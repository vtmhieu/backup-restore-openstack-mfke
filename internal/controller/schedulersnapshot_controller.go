/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	snapshotv1beta1 "gitlab.fci.vn/xplat/fke/backup-restore-openstack-mfke.git/api/v1beta1"
)

// SchedulerSnapshotReconciler reconciles a SchedulerSnapshot object
type SchedulerSnapshotReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=schedulersnapshots,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=schedulersnapshots/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=schedulersnapshots/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the SchedulerSnapshot object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.0/pkg/reconcile
func (r *SchedulerSnapshotReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// TODO(user): your logic here
	scheduleSnapshot := &snapshotv1beta1.SchedulerSnapshot{}
	// pvSnapshot := &snapshotv1beta1.PvSnapshot{}
	log.Info("Reconcile", "req", req)

	// Check existance + finalizer
	if err := r.Client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, scheduleSnapshot); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Object is gone, stop reconciling")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("error retrieving object from store: %w", err)
	}
	if !controllerutil.ContainsFinalizer(scheduleSnapshot, SnapshotFinalizerName) {
		controllerutil.AddFinalizer(scheduleSnapshot, SnapshotFinalizerName)
		if err := r.Update(ctx, scheduleSnapshot); err != nil {
			log.Error(err, "Failed to update Snapshot controller finalizer")
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, req.NamespacedName, scheduleSnapshot); err != nil {
			log.Error(err, "Failed to re-fetch Snapshot list in shoot")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if len(scheduleSnapshot.Status.Conditions) == 0 {
		meta.SetStatusCondition(&scheduleSnapshot.Status.Conditions, metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation"})
		klog.Infof("Set Status Condition of Snapshot crd %v", scheduleSnapshot.Status.Conditions)
		if err := r.Status().Update(ctx, scheduleSnapshot); err != nil {
			log.Error(err, "Failed to update Snapshot status condition")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the memcached Custom Resource after updating the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raising the error "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, scheduleSnapshot); err != nil {
			log.Error(err, "Failed to re-fetch Snapshot list")
			return ctrl.Result{}, err
		}
		klog.Infof("Fetch of Snapshot %v", scheduleSnapshot.Status.Conditions)
	}

	// define the finalizer for PVC
	if scheduleSnapshot.ObjectMeta.DeletionTimestamp.IsZero() {
		// Reconcile snapshot Schedule
		requeueAfter, err := r.ReconcileScheduleSnapshot(ctx, r.Client, scheduleSnapshot)
		if err != nil {
			klog.Info("Reconcile snapshot Failed")
			//update status
			meta.SetStatusCondition(&scheduleSnapshot.Status.Conditions, metav1.Condition{Type: "Available",
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to reconcile for the custom resource (%s): (%s)", scheduleSnapshot.Name, err)})
			if err := r.Status().Update(ctx, scheduleSnapshot); err != nil {
				log.Error(err, "Failed to update snapshot crds status")
				return ctrl.Result{RequeueAfter: requeueAfter}, err
			}
			return ctrl.Result{RequeueAfter: requeueAfter}, err
		}
		meta.SetStatusCondition(&scheduleSnapshot.Status.Conditions, metav1.Condition{Type: "Available",
			Status: metav1.ConditionTrue, Reason: "Reconciling",
			Message: fmt.Sprintf("Snapshot schedule %s in shoot %s is reconciled", scheduleSnapshot.Name, scheduleSnapshot.Namespace)})

		if err := r.Status().Update(ctx, scheduleSnapshot); err != nil {
			log.Error(err, "Failed to update snapshot crds status")
			return ctrl.Result{RequeueAfter: requeueAfter}, err
		}
		return ctrl.Result{RequeueAfter: requeueAfter}, err
	} else {
		meta.SetStatusCondition(&scheduleSnapshot.Status.Conditions, metav1.Condition{Type: "Degraded",
			Status: metav1.ConditionUnknown, Reason: "Finalizing",
			Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", scheduleSnapshot.Name)})

		if err := r.Status().Update(ctx, scheduleSnapshot); err != nil {
			log.Error(err, "Failed to update createSnapshot crds status")
			return ctrl.Result{}, err
		}
		// The object is being deleted
		ns := &corev1.Namespace{}
		err := r.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Namespace}, ns)
		if err != nil {
			log.Error(err, "unable to fetch Namespace")
			return ctrl.Result{}, err
		}
		if !ns.ObjectMeta.DeletionTimestamp.IsZero() {
			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(scheduleSnapshot, SnapshotFinalizerName)
			if err := r.Update(ctx, scheduleSnapshot); err != nil {
				return ctrl.Result{}, err
			}
		}
		if controllerutil.ContainsFinalizer(scheduleSnapshot, SnapshotFinalizerName) {
			// if err := r.delete(ctx, log, falco); err != nil {
			// 	return ctrl.Result{}, err
			// }
			// log.V(1).Info("Reconcile", "Falco is deleted successfully in shoot", req.Namespace)
			// remove our finalizer from the list and update it.
			if ok := controllerutil.RemoveFinalizer(scheduleSnapshot, SnapshotFinalizerName); !ok {
				log.Error(err, "Failed to remove finalizer for Snapshot crds")
				return ctrl.Result{Requeue: true}, nil
			}
			if err := r.Update(ctx, scheduleSnapshot); err != nil {
				return ctrl.Result{}, err
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}
}

// We traverse through each PvcSnapshotClass to check if the pvc exist
// then we check the validation of cronjob and time to do the snapshot
// Meanwhile, also do the config snapshot if the ConfigSnapshotClass is set

// In retention phase, get all the snapshot and compare if it is duetime.

func (r *SchedulerSnapshotReconciler) ReconcileScheduleSnapshot(
	ctx context.Context, c client.Client,
	scheduleSnapshot *snapshotv1beta1.SchedulerSnapshot) (time.Duration, error) {

	log := log.FromContext(ctx)
	requeueAfter := 20 * time.Minute
	//snapshotSchedulerList2Update := snapshotv1beta1.SnapshotScheduler{}

	// get namespace in seed
	namespace := scheduleSnapshot.Namespace
	clusterName := namespace[4:]
	// get kubeconfig
	shootKubeconfigDataString, err := GetSecretShootKubeconfig(ctx, c, namespace)
	if err != nil {
		return requeueAfter, fmt.Errorf("unable to get shoot secret data kubeconfig in ns %s: %v", clusterName, err)
	}
	// create shoot client set from this kubeconfig data
	shootClientSet, err := CreateShootKubeClient(ctx, shootKubeconfigDataString, clusterName)
	if err != nil {
		return requeueAfter, fmt.Errorf("unable to create shoot client set %s: %v", clusterName, err)
	}
	// get namespace in shoot
	namespaceList, err := GetNamespace(shootClientSet)
	if err != nil {
		return requeueAfter, fmt.Errorf("unable to create shoot client set %s: %v", clusterName, err)
	}
	klog.Infof("List of namespace %v", namespaceList)

	// get all PVC existing in shoot
	pvcList, err := getPVC(shootClientSet, namespaceList)
	if err != nil {
		return requeueAfter, fmt.Errorf("unable to get pvc list in shoot %s: %v", clusterName, err)
	}
	requeuAfterList := []time.Duration{}

	now := time.Now()

	// -> run through each SnapshotScheduler
	if scheduleSnapshot.Spec.Enabled {

		// ConfigSnapshot
		// Validate the cronjob time
		// Checktime -> create config snapshot
		// Get Config Snapshot list -> check retention -> delete
		if scheduleSnapshot.Spec.SnapshotScheduler.ConfigSnapshotClass.IncludeClusterResources || scheduleSnapshot.Spec.SnapshotScheduler.ConfigSnapshotClass.IncludeNamespaces != nil {
			validated := true

			for i := range scheduleSnapshot.Spec.SnapshotScheduler.Schedules {
				// check validate and convert to cron.Schedule type
				parseCron, err := ValidateCronSpec(scheduleSnapshot.Spec.SnapshotScheduler.Schedules[i].Start)
				if err != nil {
					log.Error(err, "not a valid cron job")
					validated = false
				}
				parseLocation, err := ValidateScheduleLocation(scheduleSnapshot.Spec.SnapshotScheduler.Schedules[i].Location)
				if err != nil {
					log.Error(err, "not a valid location timestamp")
					validated = false
				}
				requeuAfter := nextSnapshotDuration(parseCron, parseLocation, now)
				klog.Infof("Time to the next snapshot is: %s", requeuAfter)
				if validated {
					//snapshotSchedulerList2Update = scheduleSnapshot.Spec.SnapshotScheduler
					// -> check duration time to request
					requeuAfter := nextSnapshotDuration(parseCron, parseLocation, now)
					// -> if it is time -> run request
					// check condition of the time.Now() in compare to the previous snapshot time
					previousTime := previousSnapshotDuration(parseCron, parseLocation, now)
					klog.Infof("Time to the last snapshot is: %s", previousTime)

					if previousTime < time.Second && requeuAfter > 0 {
						// run snapshot
						err := newCreateConfigSnapshot(ctx, c, scheduleSnapshot)
						if err != nil {
							klog.Errorf("unable to create config snapshot: %s", err)
						}
						// append the next snapshot time
						requeuAfterList = append(requeuAfterList, requeuAfter)
						// append the next retention time

					} else {
						requeuAfterList = append(requeuAfterList, requeuAfter)
					}
				}
			}
			// Retention phase
			if scheduleSnapshot.Spec.SnapshotScheduler.RetentionPolicy.TimeUnits != "" {
				if scheduleSnapshot.Spec.SnapshotScheduler.RetentionPolicy.Max > 0 {
					// Get snapshot list based on namespace
					configSnapshotListReturn, err := r.getConfigSnapshotList(ctx, c)
					if err != nil {
						klog.Errorf("Unable to get configSnapshot list for Retention Reconciliation")
						// Early exit if getting snapshot list fails
					}

					maxDuration := time.Duration(scheduleSnapshot.Spec.SnapshotScheduler.RetentionPolicy.Max)
					timeUnits := scheduleSnapshot.Spec.SnapshotScheduler.RetentionPolicy.TimeUnits
					timeConversion := map[string]time.Duration{
						"minutes": time.Minute,
						"hours":   time.Hour,
						"days":    24 * time.Hour,
					}

					unitDuration, exists := timeConversion[timeUnits]
					if !exists {
						klog.Errorf("Unsupported TimeUnits: %s", timeUnits)

					}

					for _, configSnapshot := range configSnapshotListReturn.Items {
						// compare specification
						matchBackupTargets := CompareBackupTargets(configSnapshot.Spec.BackupTargets, scheduleSnapshot.Spec.SnapshotScheduler.ConfigSnapshotClass)
						if matchBackupTargets {
							// Calculate duration from creation time to now
							duration, err := calculateDuration(configSnapshot.CreationTimestamp.Format(time.RFC3339))
							if err != nil {
								klog.Errorf("Error calculating duration for config snapshot %s: %v", configSnapshot.Name, err)
								continue
							}
							klog.Infof("The duration from creation time of config snapshot %s to now is: %s", configSnapshot.Name, duration)

							// Compare duration with maxDuration
							if duration >= maxDuration*unitDuration {
								if err := newConfigDeleteSnapshot(ctx, c, configSnapshot.Name, configSnapshot.Namespace); err != nil {
									klog.Errorf("Unable to delete config snapshot %s in namespace %s due to exceed Retention",
										configSnapshot.Name, configSnapshot.Namespace)
								} else {
									klog.Infof("Successfully deleted config snapshot %s in namespace %s due to Retention",
										configSnapshot.Name, configSnapshot.Namespace)
								}
							} else {
								timeLeft := maxDuration*unitDuration - duration
								requeuAfterList = append(requeuAfterList, timeLeft)
							}
						}
					}

					// Default requeue duration
					requeueAfterDefault := getDefaultRequeueAfter(maxDuration, timeUnits)
					requeuAfterList = append(requeuAfterList, requeueAfterDefault)
				}
			}
		}

		// ==================================
		// PvcSnapshot
		for _, pvcSnapshotClass := range scheduleSnapshot.Spec.SnapshotScheduler.PvcSnapshotClass {
			// -> check pvc exist?
			pvcExisted := false
			for _, pvc := range pvcList.PVCList {
				if (pvcSnapshotClass.PvcName == pvc.PvcName) && (pvcSnapshotClass.Namespace == pvc.Namespace) {
					pvcExisted = true
					break
				}
			}
			// -> check validation of schedule
			validated := true
			if len(scheduleSnapshot.Spec.SnapshotScheduler.Schedules) != 0 {
				for i := range scheduleSnapshot.Spec.SnapshotScheduler.Schedules {
					// check validate and convert to cron.Schedule type
					parseCron, err := ValidateCronSpec(scheduleSnapshot.Spec.SnapshotScheduler.Schedules[i].Start)
					if err != nil {
						log.Error(err, "not a valid cron job")
						validated = false
					}
					parseLocation, err := ValidateScheduleLocation(scheduleSnapshot.Spec.SnapshotScheduler.Schedules[i].Location)
					if err != nil {
						log.Error(err, "not a valid location timestamp")
						validated = false
					}
					requeuAfter := nextSnapshotDuration(parseCron, parseLocation, now)
					klog.Infof("Time to the next snapshot is: %s", requeuAfter)

					if pvcExisted && validated {
						//snapshotSchedulerList2Update = scheduleSnapshot.Spec.SnapshotScheduler
						// -> check duration time to request
						requeuAfter := nextSnapshotDuration(parseCron, parseLocation, now)
						// -> if it is time -> run request
						// check condition of the time.Now() in compare to the previous snapshot time
						previousTime := previousSnapshotDuration(parseCron, parseLocation, now)
						klog.Infof("Time to the last snapshot is: %s", previousTime)

						if previousTime < 10*time.Second && requeuAfter > 0 {
							// run snapshot
							err := newCreateSnapshot(ctx, c, pvcSnapshotClass.PvcName, pvcSnapshotClass.Namespace, scheduleSnapshot.Namespace, "Scheduled")
							if err != nil {
								klog.Errorf("unable to create snapshot for persistentVolumeName %s in namespace %s: %s", pvcSnapshotClass.PvcName, pvcSnapshotClass.Namespace, err)
							}
							// append the next snapshot time
							requeuAfterList = append(requeuAfterList, requeuAfter)
							// append the next retention time

						} else {
							requeuAfterList = append(requeuAfterList, requeuAfter)
						}
					}
				}
			}
			// Check Retention Policy
			if scheduleSnapshot.Spec.SnapshotScheduler.RetentionPolicy.TimeUnits != "" {
				if scheduleSnapshot.Spec.SnapshotScheduler.RetentionPolicy.Max > 0 {
					// Get snapshot list based on namespace
					snapshotListReturn, err := r.getSnapshotList(ctx, c)
					if err != nil {
						klog.Errorf("Unable to get snapshot list in namespace %s for Retention Reconciliation", pvcSnapshotClass.Namespace)
						continue // Early exit if getting snapshot list fails
					}

					maxDuration := time.Duration(scheduleSnapshot.Spec.SnapshotScheduler.RetentionPolicy.Max)
					timeUnits := scheduleSnapshot.Spec.SnapshotScheduler.RetentionPolicy.TimeUnits
					timeConversion := map[string]time.Duration{
						"minutes": time.Minute,
						"hours":   time.Hour,
						"days":    24 * time.Hour,
					}

					unitDuration, exists := timeConversion[timeUnits]
					if !exists {
						klog.Errorf("Unsupported TimeUnits: %s", timeUnits)
						continue
					}

					for _, snapshot := range snapshotListReturn.Items {
						if snapshot.Status.SourcePvcName == pvcSnapshotClass.PvcName {
							// Calculate duration from creation time to now

							var duration time.Duration
							if snapshot.Status.CreationTime != "" && snapshot.Status.CreationTime != "N/A" {
								duration, err = calculateDuration(snapshot.Status.CreationTime)
							} else {
								duration, err = calculateDuration(snapshot.CreationTimestamp.String())
							}
							if err != nil {
								klog.Errorf("Error calculating duration for snapshot %s: %v", snapshot.Status.SnapshotName, err)
								continue
							}
							klog.Infof("The duration from creation time of Snapshot %s to now is: %s", snapshot.Status.SnapshotName, duration)

							// Compare duration with maxDuration
							if duration >= maxDuration*unitDuration {
								if snapshot.Spec.NumInUse > 0 {
									continue
								}
								if err := newDeleteSnapshot(ctx, c, snapshot.Name, snapshot.Namespace, snapshot.Spec.PvcName, snapshot.Spec.Namespace); err != nil {
									klog.Errorf("Unable to delete snapshot %s in namespace %s due to exceed Retention", snapshot.Status.SnapshotName, pvcSnapshotClass.Namespace)
								} else {
									klog.Infof("Successfully deleted snapshot %s in namespace %s due to Retention", snapshot.Status.SnapshotName, pvcSnapshotClass.Namespace)
								}
							} else {
								timeLeft := maxDuration*unitDuration - duration
								requeuAfterList = append(requeuAfterList, timeLeft)
							}
						}
					}

					// Default requeue duration
					requeueAfterDefault := getDefaultRequeueAfter(maxDuration, timeUnits)
					requeuAfterList = append(requeuAfterList, requeueAfterDefault)
				}
			}
		}
	}
	if len(requeuAfterList) != 0 {
		sort.Slice(requeuAfterList, func(i, j int) bool {
			return requeuAfterList[i] < requeuAfterList[j]
		})
		klog.Infof("the requeue list is: %v", requeuAfterList)
		// -> pick the shortest time to reconcile
		requeueAfter = requeuAfterList[0]
	}
	// -> requeue
	klog.Infof("the reconcile will requeue after: %s", requeueAfter)

	if scheduleSnapshot.Annotations[SnapshotReconcileAnnotation] == "true" {
		delete(scheduleSnapshot.Annotations, SnapshotReconcileAnnotation)
		if err := r.Update(ctx, scheduleSnapshot); err != nil {
			return requeueAfter, fmt.Errorf("error to delete annotation %s in createSnapshot resource: [%v]", DeleteSnapshotEnabledAnnotation, err)
		}
	}
	return requeueAfter, nil
}

func (r *SchedulerSnapshotReconciler) getSnapshotList(ctx context.Context, c client.Client) (snapshotv1beta1.SnapshotList, error) {
	snapshotList := &snapshotv1beta1.SnapshotList{}
	if err := c.List(ctx, snapshotList); err != nil {
		klog.Errorf("Error listing Snapshots: %s", err)
		return *snapshotList, err
	}
	return *snapshotList, nil
}

func (r *SchedulerSnapshotReconciler) getConfigSnapshotList(ctx context.Context, c client.Client) (snapshotv1beta1.CreateKubeSnapshotList, error) {
	configSnapshotList := &snapshotv1beta1.CreateKubeSnapshotList{}
	if err := c.List(ctx, configSnapshotList); err != nil {
		klog.Errorf("Error listing Snapshots: %s", err)
		return *configSnapshotList, err
	}
	return *configSnapshotList, nil
}

func newCreateSnapshot(ctx context.Context, c client.Client, pvcName string, shootNamespace string, seedNamespace string, snapshotType string) error {
	currentTimeString := convertTimeNow2String(time.Now())

	snapshot := &snapshotv1beta1.Snapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName + "-" + shootNamespace + "-" + currentTimeString,
			Namespace: seedNamespace,
		},
		Spec: snapshotv1beta1.SnapshotSpec{
			PvcName:      pvcName,
			Namespace:    shootNamespace,
			SnapshotType: snapshotType,
			NumInUse:     0,
		},
	}
	if err := c.Create(ctx, snapshot); err != nil {
		return err
	}
	return nil
}

func newCreateConfigSnapshot(ctx context.Context, c client.Client, scheduleSnapshot *snapshotv1beta1.SchedulerSnapshot) error {
	currentTimeString := convertTimeNow2String(time.Now())
	configSnapshot := &snapshotv1beta1.CreateKubeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fke-configsnapshot-" + currentTimeString,
			Namespace: scheduleSnapshot.Namespace, // seedNamespace
		},
		Spec: snapshotv1beta1.CreateKubeSnapshotSpec{
			BackupTargets: snapshotv1beta1.CreateKubeSnapshotBackupTargets{
				IncludeNamespaces:       scheduleSnapshot.Spec.SnapshotScheduler.ConfigSnapshotClass.IncludeNamespaces,
				IncludeClusterResources: scheduleSnapshot.Spec.SnapshotScheduler.ConfigSnapshotClass.IncludeClusterResources,
			},
			RunCounter: 1,
		},
	}
	if err := c.Create(ctx, configSnapshot); err != nil {
		return err
	}
	return nil
}

func newDeleteSnapshot(ctx context.Context, c client.Client, snapshotName string, seedNamespace string, pvcName string, shootNamespace string) error {
	snapshot := &snapshotv1beta1.Snapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      snapshotName,
			Namespace: seedNamespace,
		},
		Spec: snapshotv1beta1.SnapshotSpec{
			PvcName:   pvcName,
			Namespace: shootNamespace,
		},
	}
	if err := c.Delete(ctx, snapshot); err != nil {
		return err
	}

	return nil
}

func newConfigDeleteSnapshot(ctx context.Context, c client.Client, snapshotName string, seedNamespace string) error {
	configSnapshot := &snapshotv1beta1.CreateKubeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      snapshotName,
			Namespace: seedNamespace,
		},
	}
	if err := c.Delete(ctx, configSnapshot); err != nil {
		return err
	}

	return nil
}

func getDefaultRequeueAfter(maxDuration time.Duration, timeUnits string) time.Duration {
	var defaultRequeue time.Duration
	if timeUnits == Minutes {
		defaultRequeue = maxDuration * time.Minute
	} else if timeUnits == Hours {
		defaultRequeue = maxDuration * time.Hour
	} else if timeUnits == Days {
		defaultRequeue = maxDuration * 24 * time.Hour
	}
	return defaultRequeue
}

// It returns true if they are equal and false otherwise.
func CompareBackupTargets(bt1, bt2 snapshotv1beta1.CreateKubeSnapshotBackupTargets) bool {
	// Compare IncludeClusterResources field
	if bt1.IncludeClusterResources != bt2.IncludeClusterResources {
		return false
	}

	// Compare IncludeNamespaces field (order doesn't matter)
	if len(bt1.IncludeNamespaces) != len(bt2.IncludeNamespaces) {
		return false
	}

	// Sort slices to ensure order-independent comparison
	sort.Strings(bt1.IncludeNamespaces)
	sort.Strings(bt2.IncludeNamespaces)

	// Compare the sorted IncludeNamespaces slices
	return reflect.DeepEqual(bt1.IncludeNamespaces, bt2.IncludeNamespaces)
}

// SetupWithManager sets up the controller with the Manager.
func (r *SchedulerSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&snapshotv1beta1.SchedulerSnapshot{}).
		Complete(r)
}
