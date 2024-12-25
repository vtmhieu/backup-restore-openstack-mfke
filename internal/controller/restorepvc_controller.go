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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	snapshotv1beta1 "gitlab.fci.vn/xplat/fke/backup-restore-openstack-mfke.git/api/v1beta1"
)

// RestorePvcReconciler reconciles a RestorePvc object
type RestorePvcReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=restorepvcs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=restorepvcs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=restorepvcs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the RestorePvc object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.0/pkg/reconcile
func (r *RestorePvcReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	restorePvc := &snapshotv1beta1.RestorePvc{}
	log.Info("Reconcile", "req", req)

	// Check existance + finalizer
	if err := r.Client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, restorePvc); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Object is gone, stop reconciling")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("error retrieving object from store: %w", err)
	}

	if !controllerutil.ContainsFinalizer(restorePvc, RestorePVCFinalizerName) {
		controllerutil.AddFinalizer(restorePvc, RestorePVCFinalizerName)
		if err := r.Update(ctx, restorePvc); err != nil {
			log.Error(err, "Failed to update restorePvc controller finalizer")
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, req.NamespacedName, restorePvc); err != nil {
			log.Error(err, "Failed to re-fetch restorePvc list in shoot")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if len(restorePvc.Status.Conditions) == 0 {
		meta.SetStatusCondition(&restorePvc.Status.Conditions, metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation"})
		klog.Infof("Set Status Condition of restorePvc crd %v", restorePvc.Status.Conditions)
		if err := r.Status().Update(ctx, restorePvc); err != nil {
			log.Error(err, "Failed to update restorePvc status condition")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the memcached Custom Resource after updating the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raising the error "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, restorePvc); err != nil {
			log.Error(err, "Failed to re-fetch restorePvc list")
			return ctrl.Result{}, err
		}
		klog.Infof("Fetch of restorePvc %v", restorePvc.Status.Conditions)
	}

	// define the finalizer for PVC
	if restorePvc.ObjectMeta.DeletionTimestamp.IsZero() {
		returnRestorePvc, err := r.ReconcileRestorePvc(ctx, r.Client, restorePvc)

		if err := r.removeRestoreAnnotation(ctx, restorePvc); err != nil {
			log.Error(err, "Failed to remove annotation for reconcile restorePvc")
		}

		if err != nil {
			klog.Info("Reconcile restorePvc Failed")
			return r.handleRestorePvcError(ctx, restorePvc, returnRestorePvc, err)
		}

		log.V(1).Info("Reconcile", "RestorePvc list has been successfully updated", req.Namespace)
		return r.handleRestorePvcSuccess(ctx, restorePvc, returnRestorePvc)

	} else {
		meta.SetStatusCondition(&restorePvc.Status.Conditions, metav1.Condition{Type: "Degraded",
			Status: metav1.ConditionUnknown, Reason: "Finalizing",
			Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", restorePvc.Name)})

		if err := r.Status().Update(ctx, restorePvc); err != nil {
			log.Error(err, "Failed to update restorePvc crds status")
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
			controllerutil.RemoveFinalizer(restorePvc, RestorePVCFinalizerName)
			if err := r.Update(ctx, restorePvc); err != nil {
				return ctrl.Result{}, err
			}
		}
		if controllerutil.ContainsFinalizer(restorePvc, RestorePVCFinalizerName) {
			// remove our finalizer from the list and update it.
			if ok := controllerutil.RemoveFinalizer(restorePvc, RestorePVCFinalizerName); !ok {
				log.Error(err, "Failed to remove finalizer for createSnapshot crds")
				return ctrl.Result{Requeue: true}, nil
			}

			if err := r.updateDecrease_InUseSnapshot(ctx, restorePvc); err != nil {
				log.Error(err, "Failed to update decrease_InUseSnapshot for createSnapshot crds")
				return ctrl.Result{Requeue: true}, err
			}

			if err := r.Update(ctx, restorePvc); err != nil {
				return ctrl.Result{}, err
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}
}

func (r *RestorePvcReconciler) ReconcileRestorePvc(ctx context.Context, c client.Client, restorePvc *snapshotv1beta1.RestorePvc) (snapshotv1beta1.RestorePvcStatus, error) {
	RestorePvcReturn := snapshotv1beta1.RestorePvcStatus{}

	// get namespace in seed
	namespace := restorePvc.Namespace
	clusterName := namespace[4:]
	//currentTimeString := convertTimeNow2String(time.Now())

	// set pvc name to restore
	// if user does not have name for the pvc -> auto gen
	restorePVCName := restorePvc.Name

	// get kubeconfig
	shootKubeconfigDataString, err := GetSecretShootKubeconfig(ctx, c, namespace)
	if err != nil {
		return RestorePvcReturn, fmt.Errorf("unable to get shoot secret data kubeconfig in ns %s: %v", clusterName, err)
	}
	// create shoot client set from this kubeconfig data
	shootClientSet, err := CreateShootKubeClient(ctx, shootKubeconfigDataString, clusterName)
	if err != nil {
		return RestorePvcReturn, fmt.Errorf("unable to create shoot client set %s: %v", clusterName, err)
	}

	// check if restore Pvc existed
	// get Pvc in destinationNamespace
	// Check existed, compare SnapshotName, SourceNamespace -> update
	// if the name is existed, but snapshot name and sourceNamespace is not match
	// -> return create fail since the pvc name is used
	pvcListReturn, err := getPVCPerNs(shootClientSet, restorePvc.Spec.DesNamespace)
	if err != nil {
		klog.Errorf("Unable to get pvcList in namespace %s", restorePvc.Spec.DesNamespace)
	}
	for _, pvc := range pvcListReturn {
		if pvc.Name == restorePVCName {
			if pvc.Spec.DataSource != nil {
				if pvc.Spec.DataSource.Name == restorePvc.Spec.SnapshotName {
					RestorePvcReturn.CreationStatus = "Succeeded"
					RestorePvcReturn.RestorePvcName = pvc.Name
					RestorePvcReturn.Resources = pvc.Status.Capacity.Storage().String()
					RestorePvcReturn.SourceSnapshotName = pvc.Spec.DataSource.Name
					RestorePvcReturn.SourceNamespace = pvc.Namespace
					RestorePvcReturn.DesNamespace = pvc.Namespace
					RestorePvcReturn.VolumeName = pvc.Spec.VolumeName
					if pvc.Spec.StorageClassName != nil {
						RestorePvcReturn.StorageClassName = *pvc.Spec.StorageClassName
					}
					RestorePvcReturn.AccessMode = pvc.Spec.AccessModes
					if pvc.Spec.VolumeMode != nil {
						RestorePvcReturn.VolumeMode = string(*pvc.Spec.VolumeMode)
					}
					RestorePvcReturn.Status = string(pvc.Status.Phase)
					RestorePvcReturn.CreationTime = pvc.CreationTimestamp
					return RestorePvcReturn, nil
				}
			} else {
				// return the restore PVC name is used in namespace
				RestorePvcReturn = snapshotv1beta1.RestorePvcStatus{
					CreationStatus:     "Failed",
					RestorePvcName:     restorePVCName,
					Resources:          restorePvc.Spec.Storage,
					SourceSnapshotName: restorePvc.Spec.SnapshotName,
					SourceNamespace:    restorePvc.Spec.SourceNamespace,
					DesNamespace:       restorePvc.Spec.DesNamespace,
				}
				return RestorePvcReturn, fmt.Errorf("the PVC name is used")
			}
		}
	}

	// Start to restore for the first time or retry if it is failed
	// at the end, increase the number of InUse
	RestorePvcReturn, err = r.restorePvc(shootClientSet, restorePVCName, restorePvc.Spec.SourceNamespace, restorePvc.Spec.DesNamespace, restorePvc.Spec.SnapshotName, restorePvc.Spec.AccessModes, restorePvc.Spec.Storage)
	if err != nil {
		RestorePvcReturn = snapshotv1beta1.RestorePvcStatus{
			CreationStatus:     "Failed",
			RestorePvcName:     restorePVCName,
			Resources:          restorePvc.Spec.Storage,
			SourceSnapshotName: restorePvc.Spec.SnapshotName,
			SourceNamespace:    restorePvc.Spec.SourceNamespace,
			DesNamespace:       restorePvc.Spec.DesNamespace,
		}
		return RestorePvcReturn, fmt.Errorf("unable to restore pvc %s from snapshot %s in shoot %s: %v", restorePVCName, restorePvc.Spec.SnapshotName, clusterName, err)
	}

	if val, exists := restorePvc.Annotations[RestorePVCEnabledAnnotation]; exists && val == "true" {
		// Annotation exists and is "true", delete it
		delete(restorePvc.Annotations, RestorePVCEnabledAnnotation)
		if err := r.Update(ctx, restorePvc); err != nil {
			return RestorePvcReturn, fmt.Errorf("error to delete annotation %s in restorePvc resource: [%v]", RestorePVCEnabledAnnotation, err)
		}
	} else {
		// Annotation doesn't exist or is not "true"
		if RestorePvcReturn.CreationStatus != "Failed" {
			// Update spec NumInUse in snapshot resource
			if err := r.updateIncrease_InUseSnapshot(ctx, restorePvc); err != nil {
				return RestorePvcReturn, err
			}
		}
	}

	return RestorePvcReturn, nil
}

func (r *RestorePvcReconciler) removeRestoreAnnotation(ctx context.Context, restorePvc *snapshotv1beta1.RestorePvc) error {

	if restorePvc.Annotations[RestorePVCEnabledAnnotation] == "true" {
		delete(restorePvc.Annotations, RestorePVCEnabledAnnotation)

		if err := r.Update(ctx, restorePvc); err != nil {
			return err
		}
	}

	return nil
}

// restorePvc function create PVC based on the given information above, return err if not creat successfully
func (r *RestorePvcReconciler) restorePvc(shootClientSet *kubernetes.Clientset, restorePvcName string, sourceNamespace string, destinationNamespace string, snapshotName string, accessModes []corev1.PersistentVolumeAccessMode, resourceSize string) (snapshotv1beta1.RestorePvcStatus, error) {

	returnPvcStatus := snapshotv1beta1.RestorePvcStatus{}
	pvc := &corev1.PersistentVolumeClaim{}

	// Define the PersistentVolumeClaim
	// 2 case restore in the same namespace & different ns
	if sourceNamespace == destinationNamespace {
		pvc = &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      restorePvcName,
				Namespace: destinationNamespace,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				DataSource: &corev1.TypedLocalObjectReference{
					Name:     snapshotName,
					Kind:     "VolumeSnapshot",
					APIGroup: func() *string { s := "snapshot.storage.k8s.io"; return &s }(),
				},
				// DataSourceRef: &corev1.TypedObjectReference{
				// 	Name:      snapshotName,
				// 	Kind:      "VolumeSnapshot",
				// 	APIGroup:  func() *string { s := "snapshot.storage.k8s.io"; return &s }(),
				// 	Namespace: &sourceNamespace,
				// },
				// AccessModes: []corev1.PersistentVolumeAccessMode{
				// 	corev1.ReadWriteOnce,
				// },
				AccessModes: accessModes,
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse(resourceSize),
					},
				},
			},
		}
	} else {
		// create new volumesnapshotcontent from the volumesnapshot
		// get volumesnapshot from snapshotName + source Namespace -> get SnapshotContentName
		// -> get volumesnapshotcontent base on snapshotContentName -> get status.snapshotHandle
		// -> create new volumeSnapshotcontent in DesNamespace from the above snapshotHandle with DataSourceRef.wannabeVolumeSnapshotContentName + DesNamespace
		// -> create new volumeSnapshot based on this volumesnapshotContent
		// -> create new restorePvc base on this volumeSnapshot
	}

	// Create the PVC
	returnPVC, err := shootClientSet.CoreV1().PersistentVolumeClaims(destinationNamespace).Create(context.TODO(), pvc, metav1.CreateOptions{})
	if err != nil {
		fmt.Printf("Error creating PVC: %v\n", err)
		returnPvcStatus = snapshotv1beta1.RestorePvcStatus{
			CreationStatus:     "False",
			RestorePvcName:     restorePvcName,
			Resources:          resourceSize,
			SourceSnapshotName: snapshotName,
			SourceNamespace:    sourceNamespace,
			DesNamespace:       destinationNamespace,
		}
		return returnPvcStatus, err
	}

	returnPvcStatus.CreationStatus = "True"
	returnPvcStatus.RestorePvcName = returnPVC.Name
	returnPvcStatus.DesNamespace = returnPVC.Namespace
	returnPvcStatus.Resources = returnPVC.Status.Capacity.Storage().String()
	returnPvcStatus.SourceSnapshotName = snapshotName
	if returnPVC.Spec.DataSourceRef != nil {
		if returnPVC.Spec.DataSourceRef.Namespace != nil {
			returnPvcStatus.SourceNamespace = *returnPVC.Spec.DataSourceRef.Namespace
		}
	}
	returnPvcStatus.VolumeName = returnPVC.Spec.VolumeName
	if returnPVC.Spec.StorageClassName != nil {
		returnPvcStatus.StorageClassName = *returnPVC.Spec.StorageClassName
	}
	returnPvcStatus.AccessMode = returnPVC.Status.AccessModes
	if returnPVC.Spec.VolumeMode != nil {
		returnPvcStatus.VolumeMode = string(*returnPVC.Spec.VolumeMode)
	}
	returnPvcStatus.Status = string(returnPVC.Status.Phase)
	returnPvcStatus.CreationTime = returnPVC.CreationTimestamp

	return returnPvcStatus, nil
}

func (r *RestorePvcReconciler) updateRestoreStatus(ctx context.Context, snapshot *snapshotv1beta1.RestorePvc, newStatus snapshotv1beta1.RestorePvcStatus) error {
	if !r.statusEqual(snapshot.Status, newStatus) {
		snapshot.Status = newStatus
		if err := r.Status().Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to update snapshot CRD status: %v", err)
		}
	}
	return nil
}

func (r *RestorePvcReconciler) statusEqual(a, b snapshotv1beta1.RestorePvcStatus) bool {
	return a.CreationStatus == b.CreationStatus &&
		a.RestorePvcName == b.RestorePvcName &&
		a.Resources == b.Resources &&
		a.SourceSnapshotName == b.SourceSnapshotName &&
		a.SourceNamespace == b.SourceNamespace &&
		a.CreationTime == b.CreationTime &&
		a.DesNamespace == b.DesNamespace &&
		a.VolumeName == b.VolumeName &&
		a.StorageClassName == b.StorageClassName &&
		a.VolumeMode == b.VolumeMode &&
		a.Status == b.Status
}

func (r *RestorePvcReconciler) handleRestorePvcError(ctx context.Context, restorePvc *snapshotv1beta1.RestorePvc, returnRestorePvc snapshotv1beta1.RestorePvcStatus, err error) (ctrl.Result, error) {
	newStatus := r.buildRestorePvcStatus(restorePvc, returnRestorePvc, "Failed")
	if err := r.updateRestoreStatus(ctx, restorePvc, newStatus); err != nil {
		klog.Error(err, "Failed to update restorePvc CRD status")
		return ctrl.Result{RequeueAfter: requeueTime}, err
	}

	meta.SetStatusCondition(&restorePvc.Status.Conditions, metav1.Condition{
		Type:    "Available",
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: fmt.Sprintf("Failed to create restore (%s): (%s)", restorePvc.Name, err.Error()),
	})

	if err := r.Status().Update(ctx, restorePvc); err != nil {
		klog.Error(err, "Failed to update restorePvc status condition")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, err
}

func (r *RestorePvcReconciler) handleRestorePvcSuccess(ctx context.Context, restorePvc *snapshotv1beta1.RestorePvc, returnRestorePvc snapshotv1beta1.RestorePvcStatus) (ctrl.Result, error) {
	newStatus := r.buildRestorePvcStatus(restorePvc, returnRestorePvc, "Succeeded")
	klog.Infof("Status of restorePvc %v", restorePvc.Status.Conditions)

	if err := r.updateRestoreStatus(ctx, restorePvc, newStatus); err != nil {
		klog.Error(err, "Failed to update restorePvc CRD status")
		return ctrl.Result{}, err
	}

	meta.SetStatusCondition(&restorePvc.Status.Conditions, metav1.Condition{
		Type:    "Available",
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: fmt.Sprintf("RestorePvc %s in shoot %s is updated", restorePvc.Name, restorePvc.Namespace),
	})

	if err := r.Status().Update(ctx, restorePvc); err != nil {
		klog.Error(err, "Failed to update restorePvc status condition")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *RestorePvcReconciler) buildRestorePvcStatus(restorePvc *snapshotv1beta1.RestorePvc, returnRestorePvc snapshotv1beta1.RestorePvcStatus, creationStatus string) snapshotv1beta1.RestorePvcStatus {
	return snapshotv1beta1.RestorePvcStatus{
		CreationStatus:     creationStatus,
		RestorePvcName:     returnRestorePvc.RestorePvcName,
		Resources:          returnRestorePvc.Resources,
		SourceSnapshotName: returnRestorePvc.SourceSnapshotName,
		SourceNamespace:    restorePvc.Spec.SourceNamespace,
		DesNamespace:       returnRestorePvc.DesNamespace,
		VolumeName:         returnRestorePvc.VolumeName,
		StorageClassName:   returnRestorePvc.StorageClassName,
		AccessMode:         returnRestorePvc.AccessMode,
		VolumeMode:         returnRestorePvc.VolumeMode,
		Status:             returnRestorePvc.Status,
		CreationTime:       returnRestorePvc.CreationTime,
	}
}

func (r *RestorePvcReconciler) updateIncrease_InUseSnapshot(ctx context.Context, restorePvc *snapshotv1beta1.RestorePvc) error {
	snapshot := &snapshotv1beta1.Snapshot{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: restorePvc.Spec.SnapshotName, Namespace: restorePvc.Namespace}, snapshot); err != nil {
		// If the object is not found, treat it as already deleted
		if apierrors.IsNotFound(err) {
			klog.Errorf("failed to get snapshot: %v", err)
			return err
		}
		klog.Errorf("failed to get snapshot: %v", err)
		return err
	}
	// update the snapshot.Spec.NumInUse ++
	snapshot.Spec.NumInUse++

	// Update the snapshot resource in the cluster
	if err := r.Client.Update(ctx, snapshot); err != nil {
		return fmt.Errorf("failed to update snapshot NumInUse: %w", err)
	}
	return nil
}

func (r *RestorePvcReconciler) updateDecrease_InUseSnapshot(ctx context.Context, restorePvc *snapshotv1beta1.RestorePvc) error {
	snapshot := &snapshotv1beta1.Snapshot{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: restorePvc.Spec.SnapshotName, Namespace: restorePvc.Namespace}, snapshot); err != nil {
		// If the object is not found, treat it as already deleted
		if apierrors.IsNotFound(err) {
			return err
		}
		return err
	}
	// update the snapshot.Spec.NumInUse --
	if snapshot.Spec.NumInUse <= 0 {
		return nil
	}
	// update the snapshot.Spec.NumInUse --
	snapshot.Spec.NumInUse--

	// Update the snapshot resource in the cluster
	if err := r.Client.Update(ctx, snapshot); err != nil {
		return fmt.Errorf("failed to update snapshot NumInUse: %w", err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RestorePvcReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&snapshotv1beta1.RestorePvc{}).
		Complete(r)
}
