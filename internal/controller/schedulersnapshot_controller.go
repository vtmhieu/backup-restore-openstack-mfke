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

	snapshotv1beta1 "github.com/vtmhieu/backup-restore-openstack-mfke.git/api/v1beta1"
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
	if scheduleSnapshot.Status.Conditions == nil || len(scheduleSnapshot.Status.Conditions) == 0 {
		meta.SetStatusCondition(&scheduleSnapshot.Status.Conditions, metav1.Condition{Type: "Available", Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
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
		SnapshotSchedulerListReturn, requeueAfter, err := r.ReconcileScheduleSnapshot(ctx, r.Client, scheduleSnapshot)
		if err != nil {
			klog.Info("Reconcile snapshot Failed")
			//update status
			meta.SetStatusCondition(&scheduleSnapshot.Status.Conditions, metav1.Condition{Type: "Available",
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to reconcile for the custom resource (%s): (%s)", scheduleSnapshot.Name, err)})
			scheduleSnapshot.Status.SnapshotSchedulerList = SnapshotSchedulerListReturn
			if err := r.Status().Update(ctx, scheduleSnapshot); err != nil {
				log.Error(err, "Failed to update snapshot crds status")
				return ctrl.Result{RequeueAfter: requeueAfter}, err
			}
			return ctrl.Result{RequeueAfter: requeueAfter}, err
		}
		meta.SetStatusCondition(&scheduleSnapshot.Status.Conditions, metav1.Condition{Type: "Available",
			Status: metav1.ConditionTrue, Reason: "Reconciling",
			Message: fmt.Sprintf("Snapshot schedule %s in shoot %s is reconciled", scheduleSnapshot.Name, scheduleSnapshot.Namespace)})
		scheduleSnapshot.Status.SnapshotSchedulerList = SnapshotSchedulerListReturn
		//snapshot.Status.RequeueAfter = requeueAfter
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

func (r *SchedulerSnapshotReconciler) ReconcileScheduleSnapshot(ctx context.Context, c client.Client, scheduleSnapshot *snapshotv1beta1.SchedulerSnapshot) ([]snapshotv1beta1.SnapshotScheduler, time.Duration, error) {
	log := log.FromContext(ctx)
	requeueAfter := 20 * time.Minute
	snapshotSchedulerList2Update := []snapshotv1beta1.SnapshotScheduler{}

	// get namespace in seed
	namespace := scheduleSnapshot.Namespace
	clusterName := namespace[4:]
	// get kubeconfig
	shootKubeconfigDataString, err := GetSecretShootKubeconfig(ctx, c, namespace)
	if err != nil {
		return snapshotSchedulerList2Update, requeueAfter, fmt.Errorf("unable to get shoot secret data kubeconfig in ns %s: %v", clusterName, err)
	}
	// create shoot client set from this kubeconfig data
	shootClientSet, err := CreateShootKubeClient(ctx, shootKubeconfigDataString, clusterName)
	if err != nil {
		return snapshotSchedulerList2Update, requeueAfter, fmt.Errorf("unable to create shoot client set %s: %v", clusterName, err)
	}

	// dynamicClientSet, err := CreateDynamicKubeClient(ctx, shootKubeconfigDataString)
	// if err != nil {
	// 	return snapshotSchedulerList2Update, requeueAfter, fmt.Errorf("unable to create dynamic shoot client set %s: %v", clusterName, err)
	// }
	// get namespace in shoot
	namespaceList, err := GetNamespace(shootClientSet)
	if err != nil {
		return snapshotSchedulerList2Update, requeueAfter, fmt.Errorf("unable to create shoot client set %s: %v", clusterName, err)
	}
	klog.Infof("List of namespace %v", namespaceList)

	// get all PVC existing in shoot
	pvcList, err := getPVC(shootClientSet, namespaceList)
	if err != nil {
		return snapshotSchedulerList2Update, requeueAfter, fmt.Errorf("unable to get pvc list in shoot %s: %v", clusterName, err)
	}
	requeuAfterList := []time.Duration{}

	now := time.Now()

	// -> run through each SnapshotScheduler
	if len(scheduleSnapshot.Spec.SnapshotSchedulerList) != 0 {
		for _, item := range scheduleSnapshot.Spec.SnapshotSchedulerList {
			// -> check pvc exist?
			pvcExisted := false
			for _, pvc := range pvcList.PVCList {
				if (item.PvcName == pvc.PvcName) && (item.Namespace == pvc.Namespace) {
					pvcExisted = true
					break
				}
			}
			// -> check validation of schedule
			validated := true
			if len(item.Schedules) != 0 {
				for i := range item.Schedules {
					// check validate and convert to cron.Schedule type
					parseCron, err := ValidateCronSpec(item.Schedules[i].Start)
					if err != nil {
						log.Error(err, "not a valid cron job")
						validated = false
					}
					parseLocation, err := ValidateScheduleLocation(item.Schedules[i].Location)
					if err != nil {
						log.Error(err, "not a valid location timestamp")
						validated = false
					}
					requeuAfter := nextSnapshotDuration(parseCron, parseLocation, now)
					klog.Infof("Time to the next snapshot is: %s", requeuAfter)

					if pvcExisted && validated {
						snapshotSchedulerList2Update = append(snapshotSchedulerList2Update, item)
						// -> check duration time to request
						requeuAfter := nextSnapshotDuration(parseCron, parseLocation, now)
						// -> if it is time -> run request
						// check condition of the time.Now() in compare to the previous snapshot time
						previousTime := previousSnapshotDuration(parseCron, parseLocation, now)
						klog.Infof("Time to the last snapshot is: %s", previousTime)

						if previousTime < time.Second && requeuAfter > 0 {
							// run snapshot
							err := newCreateSnapshot(ctx, c, item.PvcName, item.Namespace, scheduleSnapshot.Namespace)
							if err != nil {
								klog.Errorf("unable to create snap shot %s for persistentVolumeName %s in namespace %s: %s", item.Name, item.PvcName, item.Namespace, err)
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
			// Check RETENTION -> check RetentionPolicyType -> None skip -> Duration check
			if item.RetentionPolicy.Type != "" {
				if item.RetentionPolicy.Type == snapshotv1beta1.StoreWithinDuration && item.RetentionPolicy.MaxDuration != "" {
					// get snapshot list based on namespace
					//snapshotListReturn, err := getPvSnapshotListPerNamespace(dynamicClientSet, item.Namespace)
					snapshotListReturn, err := r.getSnapshotList(ctx, c)
					if err != nil {
						klog.Errorf("Unabled to get snapshot list in namespace %s for Retention Reconcilation", item.Namespace)
					}
					for _, snapshot := range snapshotListReturn.Items {
						// check if snapshot has SourcePvcName == Scheduler PvcName
						if snapshot.Status.SourcePvcName == item.PvcName {
							// check from creation time til now + compare to MaxDuration -> delete if needed
							duration, err := calculateDuration(snapshot.Status.CreationTime)
							if err != nil {
								fmt.Println("Error calculating duration:", err)
								continue
							}
							klog.Infof("The duration from creation time of Snapshot %s til now is : %s", snapshot.Status.SnapshotName, duration)
							if item.RetentionPolicy.MaxDuration == SevenDays {
								if duration > 7*24*time.Hour || duration == 7*24*time.Hour {
									if err := newDeleteSnapshot(ctx, c, snapshot.Name, snapshot.Namespace, snapshot.Spec.PvcName, snapshot.Spec.Namespace); err != nil {
										klog.Errorf("Unabled to delete snapshot %s in namespace %s due to exceed Retention", snapshot.Status.SnapshotName, item.Namespace)
									} else {
										klog.Infof("Successfully delete snapshot %s in namespace %s due to Retention", snapshot.Status.SnapshotName, item.Namespace)
									}
								} else {
									timeleft := 7*24*time.Hour - duration
									requeuAfterList = append(requeuAfterList, timeleft)
								}
							} else if item.RetentionPolicy.MaxDuration == FifteenDays {
								if duration > 15*24*time.Hour || duration == 15*24*time.Hour {
									if err := newDeleteSnapshot(ctx, c, snapshot.Name, snapshot.Namespace, snapshot.Spec.PvcName, snapshot.Spec.Namespace); err != nil {
										klog.Errorf("Unabled to delete snapshot %s in namespace %s due to exceed Retention", snapshot.Status.SnapshotName, item.Namespace)
									} else {
										klog.Infof("Successfully delete snapshot %s in namespace %s due to Retention", snapshot.Status.SnapshotName, item.Namespace)
									}
								} else {
									timeleft := 15*24*time.Hour - duration
									requeuAfterList = append(requeuAfterList, timeleft)
								}
							} else if item.RetentionPolicy.MaxDuration == OneMonth {
								if duration > 30*24*time.Hour || duration == 30*24*time.Hour {
									if err := newDeleteSnapshot(ctx, c, snapshot.Name, snapshot.Namespace, snapshot.Spec.PvcName, snapshot.Spec.Namespace); err != nil {
										klog.Errorf("Unabled to delete snapshot %s in namespace %s due to exceed Retention", snapshot.Status.SnapshotName, item.Namespace)
									} else {
										klog.Infof("Successfully delete snapshot %s in namespace %s due to Retention", snapshot.Status.SnapshotName, item.Namespace)
									}
								} else {
									timeleft := 30*24*time.Hour - duration
									requeuAfterList = append(requeuAfterList, timeleft)
								}
							} else if item.RetentionPolicy.MaxDuration == OneHour {
								if duration > time.Hour || duration == time.Hour {
									if err := newDeleteSnapshot(ctx, c, snapshot.Name, snapshot.Namespace, snapshot.Spec.PvcName, snapshot.Spec.Namespace); err != nil {
										klog.Errorf("Unabled to delete snapshot %s in namespace %s due to exceed Retention", snapshot.Status.SnapshotName, item.Namespace)
									} else {
										klog.Infof("Successfully delete snapshot %s in namespace %s due to Retention", snapshot.Status.SnapshotName, item.Namespace)
									}
								} else {
									timeleft := time.Hour - duration
									requeuAfterList = append(requeuAfterList, timeleft)
								}
							} else if item.RetentionPolicy.MaxDuration == OneMinute {
								if duration > time.Minute || duration == time.Minute {
									if err := newDeleteSnapshot(ctx, c, snapshot.Name, snapshot.Namespace, snapshot.Spec.PvcName, snapshot.Spec.Namespace); err != nil {
										klog.Errorf("Unabled to delete snapshot %s in namespace %s due to exceed Retention", snapshot.Status.SnapshotName, item.Namespace)
									} else {
										klog.Infof("Successfully delete snapshot %s in namespace %s due to Retention", snapshot.Status.SnapshotName, item.Namespace)
									}
								} else {
									timeleft := time.Minute - duration
									requeuAfterList = append(requeuAfterList, timeleft)
								}
							}
						}
					}
					// requeueDurationDefault, in case there is a snapshot that has just been initialized above,
					// it will be requeued at the time of retention for that snapshot
					requeueAfterDefault := getDefaultRequeueAfter(item.RetentionPolicy.MaxDuration)
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
			return snapshotSchedulerList2Update, requeueAfter, fmt.Errorf("error to delete annotation %s in createSnapshot resource: [%v]", DeleteSnapshotEnabledAnnotation, err)
		}
	}
	return snapshotSchedulerList2Update, requeueAfter, nil
}

func (r *SchedulerSnapshotReconciler) getSnapshotList(ctx context.Context, c client.Client) (snapshotv1beta1.SnapshotList, error) {
	snapshotList := &snapshotv1beta1.SnapshotList{}
	if err := c.List(ctx, snapshotList); err != nil {
		klog.Errorf("Error listing Snapshots: %s", err)
		return *snapshotList, err
	}
	return *snapshotList, nil
}

func newCreateSnapshot(ctx context.Context, c client.Client, pvcName string, shootNamespace string, seedNamespace string) error {
	currentTimeString := convertTimeNow2String(time.Now())

	snapshot := &snapshotv1beta1.Snapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName + "-" + currentTimeString,
			Namespace: seedNamespace,
		},
		Spec: snapshotv1beta1.SnapshotSpec{
			PvcName:   pvcName,
			Namespace: shootNamespace,
		},
	}
	if err := c.Create(ctx, snapshot); err != nil {
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

func getDefaultRequeueAfter(MaxDuration string) time.Duration {
	var defaultRequeue time.Duration
	if MaxDuration == OneMinute {
		defaultRequeue = time.Minute
	} else if MaxDuration == OneHour {
		defaultRequeue = time.Hour
	} else if MaxDuration == SevenDays {
		defaultRequeue = 7 * 24 * time.Hour
	} else if MaxDuration == FifteenDays {
		defaultRequeue = 15 * 24 * time.Hour
	} else if MaxDuration == OneMonth {
		defaultRequeue = 30 * 24 * time.Hour
	}

	return defaultRequeue
}

// SetupWithManager sets up the controller with the Manager.
func (r *SchedulerSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&snapshotv1beta1.SchedulerSnapshot{}).
		Complete(r)
}
