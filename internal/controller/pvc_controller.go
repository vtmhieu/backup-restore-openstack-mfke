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
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/klog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	snapshotv1beta1 "gitlab.fci.vn/xplat/fke/backup-restore-openstack-mfke.git/api/v1beta1"
)

// PvcReconciler reconciles a Pvc object
type PvcReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=pvcs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=pvcs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=snapshot.mfke.io,resources=pvcs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Pvc object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.0/pkg/reconcile
func (r *PvcReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	pvc := &snapshotv1beta1.Pvc{}
	log.Info("Reconcile", "req", req)

	// Check existance + finalizer
	if err := r.Client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, pvc); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Object is gone, stop reconciling")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("error retrieving object from store: %w", err)
	}
	if !controllerutil.ContainsFinalizer(pvc, PvcFinalizerName) {
		controllerutil.AddFinalizer(pvc, PvcFinalizerName)
		if err := r.Update(ctx, pvc); err != nil {
			log.Error(err, "Failed to update pvc controller finalizer")
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, req.NamespacedName, pvc); err != nil {
			log.Error(err, "Failed to re-fetch PVC list in shoot")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if len(pvc.Status.Conditions) == 0 {
		meta.SetStatusCondition(&pvc.Status.Conditions, metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation"})
		klog.Infof("Set Status Condition of PVC crd %v", pvc.Status.Conditions)
		if err := r.Status().Update(ctx, pvc); err != nil {
			log.Error(err, "Failed to update PVC status condition")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the memcached Custom Resource after updating the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raising the error "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, pvc); err != nil {
			log.Error(err, "Failed to re-fetch PVC list")
			return ctrl.Result{}, err
		}
		klog.Infof("Fetch of PVC %v", pvc.Status.Conditions)
	}

	// define the finalizer for PVC
	if pvc.ObjectMeta.DeletionTimestamp.IsZero() {
		if err := r.ReconcilePvc(ctx, r.Client, pvc); err != nil {
			klog.Info("Reconcile PVC Failed")
			meta.SetStatusCondition(&pvc.Status.Conditions, metav1.Condition{Type: "Available",
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to reconcile for the custom resource (%s): (%s)", pvc.Name, err)})

			if err := r.Status().Update(ctx, pvc); err != nil {
				log.Error(err, "Failed to update PVC crds status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}

		// Check if the Available condition already matches the desired state
		availableCondition := meta.FindStatusCondition(pvc.Status.Conditions, "Available")
		desiredMessage := fmt.Sprintf("PVC List %s in shoot %s is updated", pvc.Name, pvc.Namespace)
		if availableCondition == nil ||
			availableCondition.Status != metav1.ConditionTrue ||
			availableCondition.Reason != "Reconciling" ||
			availableCondition.Message != desiredMessage {

			meta.SetStatusCondition(&pvc.Status.Conditions, metav1.Condition{
				Type:    "Available",
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: desiredMessage,
			})

			if err := r.Status().Update(ctx, pvc); err != nil {
				log.Error(err, "Failed to update PVC status")
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{RequeueAfter: 20 * time.Minute}, nil
	} else {
		meta.SetStatusCondition(&pvc.Status.Conditions, metav1.Condition{Type: "Degraded",
			Status: metav1.ConditionUnknown, Reason: "Finalizing",
			Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", pvc.Name)})

		if err := r.Status().Update(ctx, pvc); err != nil {
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
			controllerutil.RemoveFinalizer(pvc, PvcFinalizerName)
			if err := r.Update(ctx, pvc); err != nil {
				return ctrl.Result{}, err
			}
		}
		if controllerutil.ContainsFinalizer(pvc, PvcFinalizerName) {
			// if err := r.delete(ctx, log, falco); err != nil {
			// 	return ctrl.Result{}, err
			// }
			// log.V(1).Info("Reconcile", "Falco is deleted successfully in shoot", req.Namespace)
			// remove our finalizer from the list and update it.
			if ok := controllerutil.RemoveFinalizer(pvc, PvcFinalizerName); !ok {
				log.Error(err, "Failed to remove finalizer for PVC crds")
				return ctrl.Result{Requeue: true}, nil
			}
			if err := r.Update(ctx, pvc); err != nil {
				return ctrl.Result{}, err
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}
}

func (r *PvcReconciler) ReconcilePvc(ctx context.Context, c client.Client, pvc *snapshotv1beta1.Pvc) error {
	// get namespace in seed
	namespace := pvc.Namespace
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

	// get all PVC existing in shoot
	pvcList, err := getPVC(shootClientSet, namespaceList)
	if err != nil {
		return fmt.Errorf("unable to get pvc list in shoot %s: %v", clusterName, err)
	}
	// for _, pvc := range pvcList.PVCList {
	// 	klog.Infof("List of pvc: name %s, namespace %s, volume name: %s", pvc.PvcName, pvc.Namespace, pvc.VolumeName)
	// }

	// update status
	// Only update the status if the PVC list has changed
	if !reflect.DeepEqual(pvc.Status.PVCList, pvcList.PVCList) {
		// update status
		klog.Infof("There is different in pvc list")
		pvc.Status.PVCList = pvcList.PVCList
	}

	if pvc.Annotations[PvcReconcileAnnotation] == "true" {
		delete(pvc.Annotations, PvcReconcileAnnotation)
		if err := r.Update(ctx, pvc); err != nil {
			return fmt.Errorf("error to delete annotation %s in pvc resource: [%v]", PvcReconcileAnnotation, err)
		}
	}
	return nil
}

func getPVC(shootClientSet *kubernetes.Clientset, namespaceList []string) (snapshotv1beta1.PvcStatus, error) {
	pvcStatus := snapshotv1beta1.PvcStatus{}
	for _, ns := range namespaceList {
		pvcReturn, err := shootClientSet.CoreV1().PersistentVolumeClaims(ns).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			klog.Errorf("Unable to get PVC in shoot: %v", err)
			continue
		}
		if len(pvcReturn.Items) != 0 {
			for _, item := range pvcReturn.Items {
				pvcDetail := snapshotv1beta1.PvcDetail{}
				pvcDetail.PvcName = item.Name
				pvcDetail.Namespace = item.Namespace
				pvcDetail.VolumeName = item.Spec.VolumeName
				pvcDetail.StorageClassName = *item.Spec.StorageClassName
				pvcDetail.Resources = item.Spec.Resources.Requests.Storage().String()
				pvcDetail.AccessMode = item.Spec.AccessModes
				pvcStatus.PVCList = append(pvcStatus.PVCList, pvcDetail)
			}
		}
	}
	return pvcStatus, nil
}

func getPVCPerNs(shootClientSet *kubernetes.Clientset, sourceNs string) ([]corev1.PersistentVolumeClaim, error) {
	pvcList, err := shootClientSet.CoreV1().PersistentVolumeClaims(sourceNs).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Unable to get PVC in shoot: %v", err)
		return nil, err
	}
	return pvcList.Items, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *PvcReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&snapshotv1beta1.Pvc{}).
		Complete(r)
}
