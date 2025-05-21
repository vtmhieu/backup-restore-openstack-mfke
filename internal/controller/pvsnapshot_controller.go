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
	"k8s.io/client-go/dynamic"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	snapshotv1beta1 "gitlab.fci.vn/xplat/fke/backup-restore-openstack-mfke.git/api/v1beta1"
)

// PvSnapshotReconciler reconciles a PvSnapshot object
type PvSnapshotReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=pvsnapshots,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=pvsnapshots/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=pvsnapshots/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PvSnapshot object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.0/pkg/reconcile
func (r *PvSnapshotReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	pvSnapshot := &snapshotv1beta1.PvSnapshot{}
	log.Info("Reconcile", "req", req)

	// Check existance + finalizer
	if err := r.Client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, pvSnapshot); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Object is gone, stop reconciling")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("error retrieving object from store: %w", err)
	}
	if !controllerutil.ContainsFinalizer(pvSnapshot, PvSnapshotFinalizerName) {
		controllerutil.AddFinalizer(pvSnapshot, PvSnapshotFinalizerName)
		if err := r.Update(ctx, pvSnapshot); err != nil {
			log.Error(err, "Failed to update pvsnapshot controller finalizer")
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, req.NamespacedName, pvSnapshot); err != nil {
			log.Error(err, "Failed to re-fetch PV snapshot list in shoot")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if len(pvSnapshot.Status.Conditions) == 0 {
		meta.SetStatusCondition(&pvSnapshot.Status.Conditions, metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation"})
		klog.Infof("Set Status Condition of PVC crd %v", pvSnapshot.Status.Conditions)
		if err := r.Status().Update(ctx, pvSnapshot); err != nil {
			log.Error(err, "Failed to update PVC status condition")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the memcached Custom Resource after updating the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raising the error "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, pvSnapshot); err != nil {
			log.Error(err, "Failed to re-fetch PVC list")
			return ctrl.Result{}, err
		}
		klog.Infof("Fetch of PVC %v", pvSnapshot.Status.Conditions)
	}

	// define the finalizer for PVC
	if pvSnapshot.ObjectMeta.DeletionTimestamp.IsZero() {
		if err := r.ReconcilePvSnapshot(ctx, r.Client, pvSnapshot); err != nil {
			klog.Info("Reconcile PVC Failed")
			meta.SetStatusCondition(&pvSnapshot.Status.Conditions, metav1.Condition{Type: "Available",
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to reconcile for the custom resource (%s): (%s)", pvSnapshot.Name, err)})

			if err := r.Status().Update(ctx, pvSnapshot); err != nil {
				log.Error(err, "Failed to update PVsnapshot crds status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
		log.V(1).Info("Reconcile", "PVSnapshot list has been successfully updated", req.Namespace)
		meta.SetStatusCondition(&pvSnapshot.Status.Conditions, metav1.Condition{Type: "Available",
			Status: metav1.ConditionTrue, Reason: "Reconciling",
			Message: fmt.Sprintf("PVSnapshot List %s in shoot %s is updated", pvSnapshot.Name, pvSnapshot.Namespace)})
		klog.Infof("Status of PVC %v", pvSnapshot.Status.Conditions)
		if err := r.Status().Update(ctx, pvSnapshot); err != nil {
			log.Error(err, "Failed to update PVSnapshot crds status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: 20 * time.Minute}, nil
	} else {
		meta.SetStatusCondition(&pvSnapshot.Status.Conditions, metav1.Condition{Type: "Degraded",
			Status: metav1.ConditionUnknown, Reason: "Finalizing",
			Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", pvSnapshot.Name)})

		if err := r.Status().Update(ctx, pvSnapshot); err != nil {
			log.Error(err, "Failed to update PVC crds status")
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
			controllerutil.RemoveFinalizer(pvSnapshot, PvSnapshotFinalizerName)
			if err := r.Update(ctx, pvSnapshot); err != nil {
				return ctrl.Result{}, err
			}
		}
		if controllerutil.ContainsFinalizer(pvSnapshot, PvSnapshotFinalizerName) {
			// if err := r.delete(ctx, log, falco); err != nil {
			// 	return ctrl.Result{}, err
			// }
			// log.V(1).Info("Reconcile", "Falco is deleted successfully in shoot", req.Namespace)
			// remove our finalizer from the list and update it.
			if ok := controllerutil.RemoveFinalizer(pvSnapshot, PvSnapshotFinalizerName); !ok {
				log.Error(err, "Failed to remove finalizer for PVC crds")
				return ctrl.Result{Requeue: true}, nil
			}
			if err := r.Update(ctx, pvSnapshot); err != nil {
				return ctrl.Result{}, err
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	//pvSnapshot := &snapshotv1beta1.PvSnapshot{}
	// TODO(user): your logic here
	// TODO(user): nhan request -> contain pvc name + volumeSnapshotClassName
	// TODO : check volumeSnapshotClassName in shoot -> if not -> create in shoot
	// 		-> true -> post request create snapshot
	// status contains all snapshots in shoot
}

func (r *PvSnapshotReconciler) ReconcilePvSnapshot(ctx context.Context, c client.Client, pvSnapshot *snapshotv1beta1.PvSnapshot) error {
	// get namespace in seed
	namespace := pvSnapshot.Namespace
	clusterName := namespace[4:]
	// get kubeconfig
	shootKubeconfigDataString, err := GetSecretShootIntegratedKubeconfig(ctx, c, namespace)
	if err != nil {
		return fmt.Errorf("unable to get shoot secret data kubeconfig in ns %s: %v", clusterName, err)
	}
	// create shoot client set from this kubeconfig data
	shootClientSet, err := CreateShootKubeClient(ctx, shootKubeconfigDataString, clusterName)
	if err != nil {
		return fmt.Errorf("unable to create shoot client set %s: %v", clusterName, err)
	}
	//create dynamic client from this kubeconfig data -> send request for crds
	dynamicClientSet, err := CreateDynamicKubeClient(ctx, shootKubeconfigDataString, clusterName)
	if err != nil {
		return fmt.Errorf("unable to create dynamic shoot client set %s: %v", clusterName, err)
	}
	// get namespace in shoot
	namespaceList, err := GetNamespace(shootClientSet)
	if err != nil {
		return fmt.Errorf("unable to create shoot client set %s: %v", clusterName, err)
	}
	klog.Infof("List of namespace %v", namespaceList)

	// get all PVC existing in shoot
	pvSnapshotStatus, err := r.getPvSnapshotStatus(dynamicClientSet, namespaceList)
	if err != nil {
		return fmt.Errorf("unable to get pvc list in shoot %s: %v", clusterName, err)
	}
	// for _, pvc := range pvSnapshotList.PVCList {
	// 	klog.Infof("List of pvc: name %s, namespace %s, volume name: %s", pvc.PvcName, pvc.Namespace, pvc.VolumeName)
	// }
	// update status
	pvSnapshot.Status = pvSnapshotStatus

	if pvSnapshot.Annotations[PvSnapshotReconcileAnnotation] == "true" {
		delete(pvSnapshot.Annotations, PvSnapshotReconcileAnnotation)
		if err := r.Update(ctx, pvSnapshot); err != nil {
			return fmt.Errorf("error to delete annotation %s in pvSnapshot resource: [%v]", PvcReconcileAnnotation, err)
		}
	}
	return nil
}

func (r *PvSnapshotReconciler) getPvSnapshotStatus(dynamicClienSet *dynamic.DynamicClient, namespaceList []string) (snapshotv1beta1.PvSnapshotStatus, error) {
	pvSnapshotStatus := snapshotv1beta1.PvSnapshotStatus{}
	for _, ns := range namespaceList {
		//klog.Infof("Checking snapshot in namespace: %s", ns)
		resp, err := getVolumeSnapShotInShoot(dynamicClienSet, ns)
		if err != nil {
			fmt.Printf("Unable to get Snapshot List in shoot cluster namespace: %s", ns)
			continue
		}
		if len(resp) != 0 {
			pvSnapshotStatus.Items = append(pvSnapshotStatus.Items, resp...)
		}
	}
	return pvSnapshotStatus, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PvSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&snapshotv1beta1.PvSnapshot{}).
		Complete(r)
}
