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
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	snapshotv1beta1 "github.com/vtmhieu/backup-restore-openstack-mfke.git/api/v1beta1"
)

// CreateSnapshotReconciler reconciles a CreateSnapshot object
type CreateSnapshotReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Clock  clock.Clock
}

//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=snapshots,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=snapshots/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=snapshots/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CreateSnapshot object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.0/pkg/reconcile
func (r *CreateSnapshotReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	snapshot := &snapshotv1beta1.Snapshot{}
	// pvSnapshot := &snapshotv1beta1.PvSnapshot{}
	log.Info("Reconcile", "req", req)

	// Check existance + finalizer
	if err := r.Client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, snapshot); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Object is gone, stop reconciling")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("error retrieving object from store: %w", err)
	}
	if !controllerutil.ContainsFinalizer(snapshot, CreateSnapshotFinalizerName) {
		controllerutil.AddFinalizer(snapshot, CreateSnapshotFinalizerName)
		if err := r.Update(ctx, snapshot); err != nil {
			log.Error(err, "Failed to update createSnapshot controller finalizer")
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, req.NamespacedName, snapshot); err != nil {
			log.Error(err, "Failed to re-fetch createSnapshot list in shoot")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if snapshot.Status.Conditions == nil || len(snapshot.Status.Conditions) == 0 {
		meta.SetStatusCondition(&snapshot.Status.Conditions, metav1.Condition{Type: "Available", Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
		klog.Infof("Set Status Condition of createSnapshot crd %v", snapshot.Status.Conditions)
		if err := r.Status().Update(ctx, snapshot); err != nil {
			log.Error(err, "Failed to update createSnapshot status condition")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the memcached Custom Resource after updating the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raising the error "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, snapshot); err != nil {
			log.Error(err, "Failed to re-fetch createSnapshot list")
			return ctrl.Result{}, err
		}
		klog.Infof("Fetch of PVC %v", snapshot.Status.Conditions)
	}

	// define the finalizer for PVC
	if snapshot.ObjectMeta.DeletionTimestamp.IsZero() {
		// Create snapshot
		if snapshot.Annotations[CreateSnapshotEnabledAnnotation] == "true" {
			if err := r.ReconcileCreateSnapshot(ctx, r.Client, snapshot); err != nil {
				klog.Info("Reconcile createSnapshot Failed")
				meta.SetStatusCondition(&snapshot.Status.Conditions, metav1.Condition{Type: "Available",
					Status: metav1.ConditionFalse, Reason: "Reconciling",
					Message: fmt.Sprintf("Failed to reconcile for the custom resource (%s): (%s)", snapshot.Name, err)})

				if err := r.Status().Update(ctx, snapshot); err != nil {
					log.Error(err, "Failed to update createSnapshot crds status")
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, err
			}
			log.V(1).Info("Reconcile", "Snapshot has been successfully created", req.Namespace)
			meta.SetStatusCondition(&snapshot.Status.Conditions, metav1.Condition{Type: "Available",
				Status: metav1.ConditionTrue, Reason: "Reconciling",
				Message: fmt.Sprintf("Snapshot %s in shoot %s is created", snapshot.Spec.Create.Name, snapshot.Namespace)})
			klog.Infof("Status of createSnapshot %v", snapshot.Status.Conditions)
			if err := r.Status().Update(ctx, snapshot); err != nil {
				log.Error(err, "Failed to update createSnapshot crds status")
				return ctrl.Result{}, err
			}
		}
		// Delete snapshot
		if snapshot.Annotations[DeleteSnapshotEnabledAnnotation] == "true" {
			if err := r.ReconcileDeleteSnapshot(ctx, r.Client, snapshot); err != nil {
				klog.Info("Reconcile createSnapshot Failed")
				meta.SetStatusCondition(&snapshot.Status.Conditions, metav1.Condition{Type: "Available",
					Status: metav1.ConditionFalse, Reason: "Deleting",
					Message: fmt.Sprintf("Failed to reconcile for the custom resource (%s): (%s)", snapshot.Name, err)})

				if err := r.Status().Update(ctx, snapshot); err != nil {
					log.Error(err, "Failed to update createSnapshot crds status")
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, err
			}
			log.V(1).Info("Reconcile", "Snapshot has been successfully deleted", req.Namespace)
			meta.SetStatusCondition(&snapshot.Status.Conditions, metav1.Condition{Type: "Available",
				Status: metav1.ConditionTrue, Reason: "Reconciling",
				Message: fmt.Sprintf("Snapshot %s in shoot %s is Deleted", snapshot.Spec.Create.Name, snapshot.Namespace)})
			klog.Infof("Status of createSnapshot %v", snapshot.Status.Conditions)
			if err := r.Status().Update(ctx, snapshot); err != nil {
				log.Error(err, "Failed to update createSnapshot crds status")
				return ctrl.Result{}, err
			}
		}
		// Check schedule list
		if len(snapshot.Spec.SnapshotSchedulerList) != 0 {
			if err := r.ReconcileScheduleSnapshot(ctx, r.Client, snapshot); err != nil {
				klog.Info("Reconcile snapshot Failed")
				meta.SetStatusCondition(&snapshot.Status.Conditions, metav1.Condition{Type: "Available",
					Status: metav1.ConditionFalse, Reason: "Reconciling",
					Message: fmt.Sprintf("Failed to reconcile for the custom resource (%s): (%s)", snapshot.Name, err)})

				if err := r.Status().Update(ctx, snapshot); err != nil {
					log.Error(err, "Failed to update snapshot crds status")
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, err
			}
		}

		// -> run through each SnapshotScheduler
		// -> check validation of schedule
		// -> check pvc exist?
		// -> check duration time to request
		// -> if it is time -> run request
		// -> pick the shortest time to reconcile
		// -> requeue
		return ctrl.Result{}, nil
	} else {
		meta.SetStatusCondition(&snapshot.Status.Conditions, metav1.Condition{Type: "Degraded",
			Status: metav1.ConditionUnknown, Reason: "Finalizing",
			Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", snapshot.Name)})

		if err := r.Status().Update(ctx, snapshot); err != nil {
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
			controllerutil.RemoveFinalizer(snapshot, CreateSnapshotFinalizerName)
			if err := r.Update(ctx, snapshot); err != nil {
				return ctrl.Result{}, err
			}
		}
		if controllerutil.ContainsFinalizer(snapshot, PvcFinalizerName) {
			// if err := r.delete(ctx, log, falco); err != nil {
			// 	return ctrl.Result{}, err
			// }
			// log.V(1).Info("Reconcile", "Falco is deleted successfully in shoot", req.Namespace)
			// remove our finalizer from the list and update it.
			if ok := controllerutil.RemoveFinalizer(snapshot, CreateSnapshotFinalizerName); !ok {
				log.Error(err, "Failed to remove finalizer for createSnapshot crds")
				return ctrl.Result{Requeue: true}, nil
			}
			if err := r.Update(ctx, snapshot); err != nil {
				return ctrl.Result{}, err
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}
}

func (r *CreateSnapshotReconciler) ReconcileScheduleSnapshot(ctx context.Context, c client.Client, scheduleSnapshot *snapshotv1beta1.Snapshot) error {
	log := log.FromContext(ctx)

	snapshotSchedulerList2Update := []snapshotv1beta1.SnapshotScheduler{}

	// get namespace in seed
	namespace := scheduleSnapshot.Namespace
	clusterName := namespace[4:]
	// get kubeconfig
	shootKubeconfigDataString, err := GetSecretShootKubeconfig(ctx, c, namespace)
	if err != nil {
		return fmt.Errorf("unable to get shoot secret data kubeconfig in ns %s: %v", clusterName, err)
	}
	// create shoot client set from this kubeconfig data
	shootClientSet, err := CreateShootKubeClient(ctx, shootKubeconfigDataString, clusterName)
	if err != nil {
		return fmt.Errorf("unable to create shoot client set %s: %v", clusterName, err)
	}
	// get namespace in shoot
	namespaceList, err := GetNamespace(shootClientSet)
	if err != nil {
		return fmt.Errorf("unable to create shoot client set %s: %v", clusterName, err)
	}
	klog.Infof("List of namespace %v", namespaceList)

	// get all PVC existing in shoot
	pvcList, err := getPVC(shootClientSet, namespaceList)
	if err != nil {
		return fmt.Errorf("unable to get pvc list in shoot %s: %v", clusterName, err)
	}

	now := r.Clock.Now()

	// -> run through each SnapshotScheduler
	if len(scheduleSnapshot.Spec.SnapshotSchedulerList) != 0 {
		for _, item := range scheduleSnapshot.Spec.SnapshotSchedulerList {
			// -> check pvc exist?
			pvc_existed := false
			for _, pvc := range pvcList.PVCList {
				if item.PvcName != pvc.PvcName {
					continue
				}
				pvc_existed = true
				break
			}
			// -> check validation of schedule
			validated := false
			// check validate and convert to cron.Schedule type
			parseCron, err := ValidateCronSpec(item.Schedules.Start)
			if err != nil {
				log.Error(err, "not a valid cron job")
				validated = false
			}
			parseLocation, err := ValidateScheduleLocation(item.Schedules.Location)
			if err != nil {
				log.Error(err, "not a valid location timestamp")
				validated = false
			}
			validated = true
			if pvc_existed && validated {
				snapshotSchedulerList2Update = append(snapshotSchedulerList2Update, item)
				// -> check duration time to request
				requeuAfter := 
				// -> if it is time -> run request
			}
		}
	}

	// -> pick the shortest time to reconcile
	// -> requeue
	return nil
}

func (r *CreateSnapshotReconciler) ReconcileCreateSnapshot(ctx context.Context, c client.Client, createSnapshot *snapshotv1beta1.Snapshot) error {
	// get namespace in seed
	namespace := createSnapshot.Namespace
	clusterName := namespace[4:]
	// get kubeconfig
	shootKubeconfigDataString, err := GetSecretShootKubeconfig(ctx, c, namespace)
	if err != nil {
		return fmt.Errorf("unable to get shoot secret data kubeconfig in ns %s: %v", clusterName, err)
	}
	// // create shoot client set from this kubeconfig data
	// shootClientSet, err := CreateShootKubeClient(ctx, shootKubeconfigDataString, clusterName)
	// if err != nil {
	// 	return fmt.Errorf("unable to create shoot client set %s: %v", clusterName, err)
	// }
	// create dynamic client from this kubeconfig data -> send request for crds
	dynamicClientSet, err := CreateDynamicKubeClient(ctx, shootKubeconfigDataString)
	if err != nil {
		return fmt.Errorf("unable to create dynamic shoot client set %s: %v", clusterName, err)
	}

	// check if volumeSnapshotClasses existed
	volumeSnapshotClassesExisted, err := checkVolumeSnapshotClasses(dynamicClientSet)
	if err != nil {
		klog.Infof("Cannot get volumeSnapshotClasses crds in shoot cluster")
	}
	// not exist -> creat volume snapshot classes
	if !volumeSnapshotClassesExisted {
		_, err = createVolumeSnapshotClasses(dynamicClientSet)
		if err != nil {
			klog.Infof("The volumeSnapshotClassName already existed")
		}
	}

	// create Snapshot base on input
	err = r.createSnapshot(dynamicClientSet, createSnapshot.Spec.Create.Name, volumeSnapshotClassName, createSnapshot.Spec.Create.PvcName, createSnapshot.Spec.Create.Namespace)
	if err != nil {
		createSnapshot.Status.CreateSuccess = false
		return fmt.Errorf("unable to create snap shot %s for persistentVolumeName %s in namespace %s", createSnapshot.Spec.Create.Name, createSnapshot.Spec.Create.PvcName, createSnapshot.Spec.Create.Namespace)
	}

	createSnapshot.Status.CreateSuccess = true
	if createSnapshot.Annotations[CreateSnapshotEnabledAnnotation] == "true" {
		delete(createSnapshot.Annotations, CreateSnapshotEnabledAnnotation)
		if err := r.Update(ctx, createSnapshot); err != nil {
			return fmt.Errorf("error to delete annotation %s in createSnapshot resource: [%v]", CreateSnapshotEnabledAnnotation, err)
		}
	}
	return nil
}

func (r *CreateSnapshotReconciler) createSnapshot(dynamicClientSet *dynamic.DynamicClient, snapshotName string, volumeSnapshotClassName string, pvcName string, namespace string) error {
	// Define the GroupVersionResource for VolumeSnapshotClass
	gvr := schema.GroupVersionResource{
		Group:    "snapshot.storage.k8s.io",
		Version:  "v1",
		Resource: VolumeSnapshot,
	}

	// Create YAML content with user input
	yamlContent := fmt.Sprintf(`
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: %s
spec:
  volumeSnapshotClassName: %s
  source:
    persistentVolumeClaimName: %s
`, snapshotName, volumeSnapshotClassName, pvcName)

	// Decode the YAML content
	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(yamlContent), 100)
	obj := &unstructured.Unstructured{}
	if err := decoder.Decode(obj); err != nil {
		fmt.Printf("Error decoding YAML content: %v\n", err)
		return err
	}

	// Apply the resource to the cluster
	_, err := dynamicClientSet.Resource(gvr).Namespace(namespace).Create(context.TODO(), obj, metav1.CreateOptions{})
	if err != nil {
		fmt.Printf("Error creating resource: %v\n", err)
		return err
	}
	return nil
}

func (r *CreateSnapshotReconciler) deleteSnapshot(dynamicClientSet *dynamic.DynamicClient, snapshotName string, namespace string) error {
	// Define the GroupVersionResource for VolumeSnapshotClass
	gvr := schema.GroupVersionResource{
		Group:    "snapshot.storage.k8s.io",
		Version:  "v1",
		Resource: VolumeSnapshot,
	}
	// Apply the resource to the cluster
	err := dynamicClientSet.Resource(gvr).Namespace(namespace).Delete(context.TODO(), snapshotName, metav1.DeleteOptions{})
	if err != nil {
		fmt.Printf("Error deleting snapshot: %v\n", err)
		return err
	}
	return nil
}

func (r *CreateSnapshotReconciler) ReconcileDeleteSnapshot(ctx context.Context, c client.Client, deleteSnapshot *snapshotv1beta1.Snapshot) error {
	// get namespace in seed
	namespace := deleteSnapshot.Namespace
	clusterName := namespace[4:]
	// get kubeconfig
	shootKubeconfigDataString, err := GetSecretShootKubeconfig(ctx, c, namespace)
	if err != nil {
		return fmt.Errorf("unable to get shoot secret data kubeconfig in ns %s: %v", clusterName, err)
	}
	// // create shoot client set from this kubeconfig data
	// shootClientSet, err := CreateShootKubeClient(ctx, shootKubeconfigDataString, clusterName)
	// if err != nil {
	// 	return fmt.Errorf("unable to create shoot client set %s: %v", clusterName, err)
	// }
	// create dynamic client from this kubeconfig data -> send request for crds
	dynamicClientSet, err := CreateDynamicKubeClient(ctx, shootKubeconfigDataString)
	if err != nil {
		return fmt.Errorf("unable to create dynamic shoot client set %s: %v", clusterName, err)
	}
	// delete Snapshot base on input
	err = r.deleteSnapshot(dynamicClientSet, deleteSnapshot.Spec.Delete.Name, deleteSnapshot.Spec.Delete.Namespace)
	if err != nil {
		deleteSnapshot.Status.CreateSuccess = false
		return fmt.Errorf("unable to delete snapshot %s  in namespace %s", deleteSnapshot.Spec.Delete.Name, deleteSnapshot.Spec.Delete.Namespace)
	}

	deleteSnapshot.Status.DeleteSuccess = true
	if deleteSnapshot.Annotations[DeleteSnapshotEnabledAnnotation] == "true" {
		delete(deleteSnapshot.Annotations, DeleteSnapshotEnabledAnnotation)
		if err := r.Update(ctx, deleteSnapshot); err != nil {
			return fmt.Errorf("error to delete annotation %s in createSnapshot resource: [%v]", DeleteSnapshotEnabledAnnotation, err)
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CreateSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&snapshotv1beta1.Snapshot{}).
		Complete(r)
}
