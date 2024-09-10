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

	snapshotv1beta1 "gitlab.fci.vn/xplat/fke/backup-restore-openstack-mfke.git/api/v1beta1"
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

	if !controllerutil.ContainsFinalizer(snapshot, SnapshotFinalizerName) {
		controllerutil.AddFinalizer(snapshot, SnapshotFinalizerName)
		if err := r.Update(ctx, snapshot); err != nil {
			log.Error(err, "Failed to update Snapshot controller finalizer")
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, req.NamespacedName, snapshot); err != nil {
			log.Error(err, "Failed to re-fetch Snapshot list in shoot")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if len(snapshot.Status.Conditions) == 0 {
		meta.SetStatusCondition(&snapshot.Status.Conditions, metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation"})
		klog.Infof("Set Status Condition of Snapshot crd %v", snapshot.Status.Conditions)
		if err := r.Status().Update(ctx, snapshot); err != nil {
			log.Error(err, "Failed to update Snapshot status condition")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the memcached Custom Resource after updating the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raising the error "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, snapshot); err != nil {
			log.Error(err, "Failed to re-fetch Snapshot list")
			return ctrl.Result{}, err
		}
		klog.Infof("Fetch of Snapshot %v", snapshot.Status.Conditions)
	}

	// define the finalizer for PVC
	if snapshot.ObjectMeta.DeletionTimestamp.IsZero() {
		// CREATE snapshot
		snapshotReturn, err := r.ReconcileCreateSnapshot(ctx, r.Client, snapshot)

		if err != nil {
			klog.Info("Reconcile createSnapshot Failed")
			return r.handleSnapshotError(ctx, snapshot, snapshotReturn, err)
		}

		log.V(1).Info("Reconcile", "Snapshot has been successfully created", req.Namespace)
		return r.handleSnapshotSuccess(ctx, snapshot, snapshotReturn)

	} else {
		// Set status to Degraded while performing finalizer operations
		r.setStatusCondition(snapshot, metav1.ConditionUnknown, "Finalizing",
			fmt.Sprintf("Performing finalizer operations for the custom resource: %s", snapshot.Name))

		if err := r.Status().Update(ctx, snapshot); err != nil {
			log.Error(err, "Failed to update Snapshot CRD status")
			return ctrl.Result{}, err
		}

		// Check if the namespace is being deleted
		ns, err := r.getNamespace(ctx, req.Namespace)
		if err != nil {
			log.Error(err, "Unable to fetch Namespace")
			return ctrl.Result{}, err
		}

		if !ns.ObjectMeta.DeletionTimestamp.IsZero() {
			return r.removeFinalizerAndUpdate(ctx, snapshot)
		}

		// Handle snapshot deletion if finalizer is present
		if controllerutil.ContainsFinalizer(snapshot, SnapshotFinalizerName) {
			return r.deleteSnapshotWithFinalizer(ctx, snapshot, req)
		}

		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}
}

// This reconcile flow check the snapshot if it existed in shoot, if it is not existed then create a new snapshot base on the object name
func (r *CreateSnapshotReconciler) ReconcileCreateSnapshot(ctx context.Context, c client.Client, createSnapshot *snapshotv1beta1.Snapshot) (snapshotv1beta1.PvSnapshotItem, error) {
	SnapshotReturn := snapshotv1beta1.PvSnapshotItem{}
	// get namespace in seed
	namespace := createSnapshot.Namespace
	clusterName := namespace[4:]
	// get kubeconfig
	shootKubeconfigDataString, err := GetSecretShootKubeconfig(ctx, c, namespace)
	if err != nil {
		return SnapshotReturn, fmt.Errorf("unable to get shoot secret data kubeconfig in ns %s: %v", clusterName, err)
	}
	// create dynamic client from this kubeconfig data -> send request for crds
	dynamicClientSet, err := CreateDynamicKubeClient(ctx, shootKubeconfigDataString)
	if err != nil {
		return SnapshotReturn, fmt.Errorf("unable to create dynamic shoot client set %s: %v", clusterName, err)
	}

	// check if snapshot existed?
	// get snapshot list based on namespace
	snapshotListReturn, err := getPvSnapshotListPerNamespace(dynamicClientSet, createSnapshot.Spec.Namespace)
	if err != nil {
		klog.Errorf("Unabled to get snapshot list in namespace %s ", createSnapshot.Spec.Namespace)
	}

	for _, snapshot := range snapshotListReturn {
		if snapshot.SnapshotName == createSnapshot.Name &&
			snapshot.SourcePvcName == createSnapshot.Spec.PvcName {
			if createSnapshot.Annotations[CreateSnapshotEnabledAnnotation] == "true" {
				delete(createSnapshot.Annotations, CreateSnapshotEnabledAnnotation)
				if err := r.Update(ctx, createSnapshot); err != nil {
					return snapshot, fmt.Errorf("error to delete annotation %s in createSnapshot resource: [%v]", CreateSnapshotEnabledAnnotation, err)
				}
			}
			return snapshot, err
		}
	}

	// since the snapshot is not existed -> create new snapshot

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
	err = r.createSnapshot(dynamicClientSet, createSnapshot.Name, volumeSnapshotClassName, createSnapshot.Spec.PvcName, createSnapshot.Spec.Namespace)
	if err != nil {
		klog.Errorf("unable to create snapshot for persistentVolumeName %s in namespace %s", createSnapshot.Spec.PvcName, createSnapshot.Spec.Namespace)
	}

	if createSnapshot.Annotations[CreateSnapshotEnabledAnnotation] == "true" {
		delete(createSnapshot.Annotations, CreateSnapshotEnabledAnnotation)
		if err := r.Update(ctx, createSnapshot); err != nil {
			return SnapshotReturn, fmt.Errorf("error to delete annotation %s in createSnapshot resource: [%v]", CreateSnapshotEnabledAnnotation, err)
		}
	}
	return SnapshotReturn, err
}

func (r *CreateSnapshotReconciler) createSnapshot(dynamicClientSet *dynamic.DynamicClient, snapshotName string, volumeSnapshotClassName string, pvcName string, namespace string) error {
	// Define the GroupVersionResource for VolumeSnapshotClass
	gvr := schema.GroupVersionResource{
		Group:    "snapshot.storage.k8s.io",
		Version:  "v1",
		Resource: VolumeSnapshot,
	}
	//currentTimeString := convertTimeNow2String(time.Now())

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

func deleteSnapshot(dynamicClientSet *dynamic.DynamicClient, snapshotName string, namespace string) error {
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

func (r *CreateSnapshotReconciler) DeleteSnapshot(ctx context.Context, c client.Client, DeleteSnapshot *snapshotv1beta1.Snapshot) error {
	// get namespace in seed
	namespace := DeleteSnapshot.Namespace
	clusterName := namespace[4:]
	// get kubeconfig
	shootKubeconfigDataString, err := GetSecretShootKubeconfig(ctx, c, namespace)
	if err != nil {
		return fmt.Errorf("unable to get shoot secret data kubeconfig in ns %s: %v", clusterName, err)
	}
	// create dynamic client from this kubeconfig data -> send request for crds
	dynamicClientSet, err := CreateDynamicKubeClient(ctx, shootKubeconfigDataString)
	if err != nil {
		return fmt.Errorf("unable to create dynamic shoot client set %s: %v", clusterName, err)
	}

	// check if snapshot existed?
	// get snapshot list based on namespace
	snapshotListReturn, err := getPvSnapshotListPerNamespace(dynamicClientSet, DeleteSnapshot.Spec.Namespace)
	if err != nil {
		klog.Errorf("Unabled to get snapshot list in namespace %s ", DeleteSnapshot.Spec.Namespace)
	}

	for _, snapshot := range snapshotListReturn {
		if snapshot.SnapshotName == DeleteSnapshot.Name && snapshot.Namespace == DeleteSnapshot.Spec.Namespace {
			// delete Snapshot base on input
			err = deleteSnapshot(dynamicClientSet, DeleteSnapshot.Name, DeleteSnapshot.Spec.Namespace)
			if err != nil {
				klog.Errorf("unable to delete snapshot %s  in namespace %s", DeleteSnapshot.Spec.SnapshotName, DeleteSnapshot.Spec.Namespace)
				return err
			}
		}
	}

	return err
}

func (r *CreateSnapshotReconciler) updateSnapshotStatus(ctx context.Context, snapshot *snapshotv1beta1.Snapshot, newStatus snapshotv1beta1.SnapshotStatus) error {
	if !statusEqual(snapshot.Status, newStatus) {
		snapshot.Status = newStatus
		if err := r.Status().Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to update snapshot CRD status: %v", err)
		}
	}
	return nil
}

func statusEqual(a, b snapshotv1beta1.SnapshotStatus) bool {
	return a.SourcePvcName == b.SourcePvcName &&
		a.SnapshotName == b.SnapshotName &&
		a.Namespace == b.Namespace &&
		a.VolumeSnapshotClassName == b.VolumeSnapshotClassName &&
		a.SnapshotContentName == b.SnapshotContentName &&
		a.CreationTime == b.CreationTime &&
		b.CreationTime != "N/A" &&
		a.ReadyToUse == b.ReadyToUse &&
		a.RestoreSize == b.RestoreSize &&
		a.CreationStatus == b.CreationStatus
}

func (r *CreateSnapshotReconciler) handleSnapshotError(
	ctx context.Context, snapshot *snapshotv1beta1.Snapshot,
	snapshotReturn snapshotv1beta1.PvSnapshotItem, err error) (ctrl.Result, error) {
	newStatus := r.buildSnapshotStatus(snapshot, snapshotReturn, "Failed")
	if err := r.updateSnapshotStatus(ctx, snapshot, newStatus); err != nil {
		klog.Error(err, "Failed to update snapshot CRD status")
		return ctrl.Result{}, err
	}

	meta.SetStatusCondition(
		&snapshot.Status.Conditions,
		metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionFalse,
			Reason:  "Create",
			Message: fmt.Sprintf("Failed to create snapshot for the PVC (%s): (%s)", snapshot.Spec.PvcName, err.Error()),
		})

	if err := r.Status().Update(ctx, snapshot); err != nil {
		klog.Error(err, "Failed to update Snapshot status condition")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, err
}

func (r *CreateSnapshotReconciler) handleSnapshotSuccess(ctx context.Context,
	snapshot *snapshotv1beta1.Snapshot, snapshotReturn snapshotv1beta1.PvSnapshotItem) (ctrl.Result, error) {
	newStatus := r.buildSnapshotStatus(snapshot, snapshotReturn, "Succeeded")
	if err := r.updateSnapshotStatus(ctx, snapshot, newStatus); err != nil {
		klog.Error(err, "Failed to update snapshot CRD status")
		return ctrl.Result{}, err
	}

	meta.SetStatusCondition(&snapshot.Status.Conditions, metav1.Condition{
		Type:    "Available",
		Status:  metav1.ConditionTrue,
		Reason:  "Create",
		Message: fmt.Sprintf("Snapshot in shoot %s is created successfully", snapshot.Namespace),
	})

	if err := r.Status().Update(ctx, snapshot); err != nil {
		klog.Error(err, "Failed to update Snapshot status condition")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *CreateSnapshotReconciler) buildSnapshotStatus(snapshot *snapshotv1beta1.Snapshot,
	snapshotReturn snapshotv1beta1.PvSnapshotItem, creationStatus string) snapshotv1beta1.SnapshotStatus {
	return snapshotv1beta1.SnapshotStatus{
		SourcePvcName:           snapshot.Spec.PvcName,
		SnapshotName:            snapshot.Name,
		Namespace:               snapshot.Spec.Namespace,
		VolumeSnapshotClassName: snapshotReturn.VolumeSnapshotClassName,
		SnapshotContentName:     snapshotReturn.SnapshotContentName,
		CreationTime:            snapshotReturn.CreationTime,
		ReadyToUse:              snapshotReturn.ReadyToUse,
		RestoreSize:             snapshotReturn.RestoreSize,
		CreationStatus:          creationStatus,
		SnapshotType:            snapshot.Spec.SnapshotType,
	}
}

func (r *CreateSnapshotReconciler) setStatusCondition(snapshot *snapshotv1beta1.Snapshot, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&snapshot.Status.Conditions, metav1.Condition{
		Type:    "Degraded",
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func (r *CreateSnapshotReconciler) getNamespace(ctx context.Context, namespace string) (*corev1.Namespace, error) {
	ns := &corev1.Namespace{}
	err := r.Get(ctx, types.NamespacedName{Name: namespace}, ns)
	return ns, err
}

func (r *CreateSnapshotReconciler) removeFinalizerAndUpdate(ctx context.Context, snapshot *snapshotv1beta1.Snapshot) (ctrl.Result, error) {
	controllerutil.RemoveFinalizer(snapshot, SnapshotFinalizerName)
	if err := r.Update(ctx, snapshot); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *CreateSnapshotReconciler) deleteSnapshotWithFinalizer(ctx context.Context, snapshot *snapshotv1beta1.Snapshot, req ctrl.Request) (ctrl.Result, error) {
	if err := r.DeleteSnapshot(ctx, r.Client, snapshot); err != nil {
		return ctrl.Result{}, err
	}
	klog.V(1).Info("Reconcile", "Snapshot is deleted successfully in shoot", req.Namespace)

	if ok := controllerutil.RemoveFinalizer(snapshot, SnapshotFinalizerName); !ok {
		klog.Error(nil, "Failed to remove finalizer for Snapshot CRD")
		return ctrl.Result{Requeue: true}, nil
	}

	if err := r.Update(ctx, snapshot); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CreateSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&snapshotv1beta1.Snapshot{}).
		Complete(r)
}
