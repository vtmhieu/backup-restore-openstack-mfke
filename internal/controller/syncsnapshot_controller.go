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

// SyncSnapshotReconciler reconciles a SyncSnapshot object
type SyncSnapshotReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=syncsnapshots,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=syncsnapshots/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=syncsnapshots/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the SyncSnapshot object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.0/pkg/reconcile
func (r *SyncSnapshotReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	syncSnapshot := &snapshotv1beta1.SyncSnapshot{}
	log.Info("Reconcile", "req", req)

	// Check existance + finalizer
	if err := r.Client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, syncSnapshot); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Object is gone, stop reconciling")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("error retrieving object from store: %w", err)
	}
	if !controllerutil.ContainsFinalizer(syncSnapshot, SyncSnapshotFinalizerName) {
		controllerutil.AddFinalizer(syncSnapshot, SyncSnapshotFinalizerName)
		if err := r.Update(ctx, syncSnapshot); err != nil {
			log.Error(err, "Failed to update restorePvc controller finalizer")
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, req.NamespacedName, syncSnapshot); err != nil {
			log.Error(err, "Failed to re-fetch syncSnapshot list in shoot")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if len(syncSnapshot.Status.Conditions) == 0 {
		meta.SetStatusCondition(&syncSnapshot.Status.Conditions, metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation"})
		klog.Infof("Set Status Condition of restorePvc crd %v", syncSnapshot.Status.Conditions)
		if err := r.Status().Update(ctx, syncSnapshot); err != nil {
			log.Error(err, "Failed to update restorePvc status condition")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the memcached Custom Resource after updating the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raising the error "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, syncSnapshot); err != nil {
			log.Error(err, "Failed to re-fetch restorePvc list")
			return ctrl.Result{}, err
		}
		klog.Infof("Fetch of restorePvc %v", syncSnapshot.Status.Conditions)
	}

	if syncSnapshot.ObjectMeta.DeletionTimestamp.IsZero() {
		missingSnapshots, extraSnapshots, err := r.ReconcileSyncSnapshot(ctx, r.Client, syncSnapshot)
		if err != nil {
			klog.Info("Reconcile PVC Failed")
			meta.SetStatusCondition(&syncSnapshot.Status.Conditions, metav1.Condition{Type: "Available",
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to reconcile for the custom resource (%s): (%s)", syncSnapshot.Name, err)})
			syncSnapshot.Status.MissingSnapshot = missingSnapshots
			syncSnapshot.Status.ExtraSnapshot = extraSnapshots
			if err := r.Status().Update(ctx, syncSnapshot); err != nil {
				log.Error(err, "Failed to update PVsnapshot crds status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
		log.V(1).Info("Reconcile", "PVSnapshot list has been successfully updated", req.Namespace)
		meta.SetStatusCondition(&syncSnapshot.Status.Conditions, metav1.Condition{Type: "Available",
			Status: metav1.ConditionTrue, Reason: "Reconciling",
			Message: fmt.Sprintf("PVSnapshot List %s in shoot %s is updated", syncSnapshot.Name, syncSnapshot.Namespace)})
		syncSnapshot.Status.MissingSnapshot = missingSnapshots
		syncSnapshot.Status.ExtraSnapshot = extraSnapshots
		if err := r.Status().Update(ctx, syncSnapshot); err != nil {
			log.Error(err, "Failed to update PVSnapshot crds status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 20 * time.Minute}, nil
	} else {
		meta.SetStatusCondition(&syncSnapshot.Status.Conditions, metav1.Condition{Type: "Degraded",
			Status: metav1.ConditionUnknown, Reason: "Finalizing",
			Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", syncSnapshot.Name)})

		if err := r.Status().Update(ctx, syncSnapshot); err != nil {
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
			controllerutil.RemoveFinalizer(syncSnapshot, SyncSnapshotFinalizerName)
			if err := r.Update(ctx, syncSnapshot); err != nil {
				return ctrl.Result{}, err
			}
		}
		if controllerutil.ContainsFinalizer(syncSnapshot, SyncSnapshotFinalizerName) {
			if ok := controllerutil.RemoveFinalizer(syncSnapshot, SyncSnapshotFinalizerName); !ok {
				log.Error(err, "Failed to remove finalizer for createSnapshot crds")
				return ctrl.Result{Requeue: true}, nil
			}
			if err := r.Update(ctx, syncSnapshot); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}
}

func (r *SyncSnapshotReconciler) ReconcileSyncSnapshot(ctx context.Context, c client.Client, syncSnapshot *snapshotv1beta1.SyncSnapshot) ([]snapshotv1beta1.SnapshotStatus, []snapshotv1beta1.SnapshotStatus, error) {

	missingSnapshots := []snapshotv1beta1.SnapshotStatus{}
	extraSnapshots := []snapshotv1beta1.SnapshotStatus{}
	// get namespace in seed
	namespace := syncSnapshot.Namespace
	clusterName := namespace[4:]
	// get kubeconfig
	shootKubeconfigDataString, err := GetSecretShootKubeconfig(ctx, c, namespace)
	if err != nil {
		return missingSnapshots, extraSnapshots, fmt.Errorf("unable to get shoot secret data kubeconfig in ns %s: %v", clusterName, err)
	}
	// create shoot client set from this kubeconfig data
	shootClientSet, err := CreateShootKubeClient(ctx, shootKubeconfigDataString, clusterName)
	if err != nil {
		return missingSnapshots, extraSnapshots, fmt.Errorf("unable to create shoot client set %s: %v", clusterName, err)
	}
	//create dynamic client from this kubeconfig data -> send request for crds
	dynamicClientSet, err := CreateDynamicKubeClient(ctx, shootKubeconfigDataString)
	if err != nil {
		return missingSnapshots, extraSnapshots, fmt.Errorf("unable to create dynamic shoot client set %s: %v", clusterName, err)
	}
	// get namespace in shoot
	namespaceList, err := GetNamespace(shootClientSet)
	if err != nil {
		return missingSnapshots, extraSnapshots, fmt.Errorf("unable to create shoot client set %s: %v", clusterName, err)
	}
	klog.Infof("List of namespace %v", namespaceList)

	// get list of snapshot in seed and shoot
	seedSnapshotList, err := getSeedSnapshotList(ctx, c)
	if err != nil {
		return missingSnapshots, extraSnapshots, fmt.Errorf("unable to get pvc list in seed %s: %v", clusterName, err)
	}

	shootSnapshotList, err := getPvSnapshotStatus(dynamicClientSet, namespaceList)
	if err != nil {
		return missingSnapshots, extraSnapshots, fmt.Errorf("unable to get pvc list in shoot %s: %v", clusterName, err)
	}

	// convert into map
	seedSnapshotMap := make(map[string]snapshotv1beta1.SnapshotStatus)
	shootSnapshotMap := make(map[string]snapshotv1beta1.SnapshotStatus)

	// convert shootSnapshotList into map
	for _, snapshot := range shootSnapshotList {
		key := fmt.Sprintf("%s/%s", snapshot.Namespace, snapshot.SnapshotName)
		shootSnapshotMap[key] = snapshot
	}

	// convert seedSnapshotList into map
	for _, snapshot := range seedSnapshotList.Items {
		key := fmt.Sprintf("%s/%s", snapshot.Status.Namespace, snapshot.Status.SnapshotName)
		seedSnapshotMap[key] = snapshot.Status
	}

	// find MissingSnapshots -> existed in shoot but not in seed -> create snapshot
	for key, snapshot := range shootSnapshotMap {
		if _, exists := seedSnapshotMap[key]; !exists {
			// only synce the snapshot which has volumeSnapshotClassname = csi-cinder-snapclass
			if snapshot.VolumeSnapshotClassName == "csi-cinder-snapclass" {
				missingSnapshots = append(missingSnapshots, snapshot)
				err := r.newCreateSnapshot(ctx, c, snapshot.SnapshotName, snapshot.SourcePvcName, snapshot.Namespace, syncSnapshot.Namespace, "Manually")
				if err != nil {
					klog.Errorf("[seed] unable to sync snapshot %s for persistentVolumeName %s in namespace %s: %s", snapshot.SnapshotName, snapshot.SourcePvcName, snapshot.Namespace, err)
				}
			}
		}
	}

	// find ExtraSnapshots: existed in seed but not in shoot
	for key, snapshot := range seedSnapshotMap {
		if _, exists := shootSnapshotMap[key]; !exists {
			extraSnapshots = append(extraSnapshots, snapshot)
			err := r.newDeleteSnapshot(ctx, c, snapshot.SnapshotName, syncSnapshot.Namespace, snapshot.SourcePvcName, snapshot.Namespace)
			if err != nil {
				klog.Errorf("[shoot] unable to sync snapshot %s for persistentVolumeName %s in namespace %s: %s", snapshot.SnapshotName, snapshot.SourcePvcName, snapshot.Namespace, err)
			}
		}
	}

	if syncSnapshot.Annotations[SyncSnapshotReconcilerAnnotation] == "true" {
		delete(syncSnapshot.Annotations, SyncSnapshotReconcilerAnnotation)
		if err := r.Update(ctx, syncSnapshot); err != nil {
			return missingSnapshots, extraSnapshots, fmt.Errorf("error to delete annotation %s in pvSnapshot resource: [%v]", SyncSnapshotReconcilerAnnotation, err)
		}
	}

	return missingSnapshots, extraSnapshots, nil
}

func (r *SyncSnapshotReconciler) newCreateSnapshot(ctx context.Context, c client.Client, snapshotName string, pvcName string, shootNamespace string, seedNamespace string, snapshotType string) error {
	snapshot := &snapshotv1beta1.Snapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      snapshotName,
			Namespace: seedNamespace,
		},
		Spec: snapshotv1beta1.SnapshotSpec{
			PvcName:      pvcName,
			Namespace:    shootNamespace,
			SnapshotType: snapshotType,
		},
	}
	if err := c.Create(ctx, snapshot); err != nil {
		return err
	}
	return nil
}

func (r *SyncSnapshotReconciler) newDeleteSnapshot(ctx context.Context, c client.Client, snapshotName string, seedNamespace string, pvcName string, shootNamespace string) error {
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

// SetupWithManager sets up the controller with the Manager.
func (r *SyncSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&snapshotv1beta1.SyncSnapshot{}).
		Complete(r)
}
