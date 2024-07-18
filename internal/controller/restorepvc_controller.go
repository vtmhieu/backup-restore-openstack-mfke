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

	snapshotv1beta1 "github.com/vtmhieu/backup-restore-openstack-mfke.git/api/v1beta1"
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
	if restorePvc.Status.Conditions == nil || len(restorePvc.Status.Conditions) == 0 {
		meta.SetStatusCondition(&restorePvc.Status.Conditions, metav1.Condition{Type: "Available", Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
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
		if err := r.ReconcileRestorePvc(ctx, r.Client, restorePvc); err != nil {
			klog.Info("Reconcile restorePvc Failed")
			meta.SetStatusCondition(&restorePvc.Status.Conditions, metav1.Condition{Type: "Available",
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to reconcile for the custom resource (%s): (%s)", restorePvc.Name, err)})

			if err := r.Status().Update(ctx, restorePvc); err != nil {
				log.Error(err, "Failed to update restorePvc crds status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
		log.V(1).Info("Reconcile", "RestorePvc list has been successfully updated", req.Namespace)
		meta.SetStatusCondition(&restorePvc.Status.Conditions, metav1.Condition{Type: "Available",
			Status: metav1.ConditionTrue, Reason: "Reconciling",
			Message: fmt.Sprintf("RestorePvc List %s in shoot %s is updated", restorePvc.Name, restorePvc.Namespace)})
		klog.Infof("Status of restorePvc %v", restorePvc.Status.Conditions)
		if err := r.Status().Update(ctx, restorePvc); err != nil {
			log.Error(err, "Failed to update restorePvc crds status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: 20 * time.Minute}, nil
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
			controllerutil.RemoveFinalizer(restorePvc, PvcFinalizerName)
			if err := r.Update(ctx, restorePvc); err != nil {
				return ctrl.Result{}, err
			}
		}
		if controllerutil.ContainsFinalizer(restorePvc, PvcFinalizerName) {
			// if err := r.delete(ctx, log, falco); err != nil {
			// 	return ctrl.Result{}, err
			// }
			// log.V(1).Info("Reconcile", "Falco is deleted successfully in shoot", req.Namespace)
			// remove our finalizer from the list and update it.
			if ok := controllerutil.RemoveFinalizer(restorePvc, PvcFinalizerName); !ok {
				log.Error(err, "Failed to remove finalizer for createSnapshot crds")
				return ctrl.Result{Requeue: true}, nil
			}
			if err := r.Update(ctx, restorePvc); err != nil {
				return ctrl.Result{}, err
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}
}

func (r *RestorePvcReconciler) ReconcileRestorePvc(ctx context.Context, c client.Client, restorePvc *snapshotv1beta1.RestorePvc) error {
	// get namespace in seed
	namespace := restorePvc.Namespace
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
	err = r.restorePvc(shootClientSet, restorePvc.Spec.RestorePvcName, restorePvc.Spec.Namespace, restorePvc.Spec.SnapshotName, restorePvc.Spec.AccessModes, restorePvc.Spec.Storage)
	if err != nil {
		restorePvc.Status.Success = false
		return fmt.Errorf("unable to restore pvc %s from snapshot %s in shoot %s: %v", restorePvc.Spec.RestorePvcName, restorePvc.Spec.SnapshotName, clusterName, err)
	}
	restorePvc.Status.Success = true
	if restorePvc.Annotations[RestorePVCReconcileAnnotation] == "true" {
		delete(restorePvc.Annotations, RestorePVCReconcileAnnotation)
		if err := r.Update(ctx, restorePvc); err != nil {
			return fmt.Errorf("error to delete annotation %s in restorePvc resource: [%v]", RestorePVCReconcileAnnotation, err)
		}
	}
	return nil
}

func (r *RestorePvcReconciler) restorePvc(shootClientSet *kubernetes.Clientset, restorePvcName string, namespace string, snapshotName string, accessModes []corev1.PersistentVolumeAccessMode, resourceSize string) error {

	// Define the PersistentVolumeClaim
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restorePvcName,
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			DataSource: &corev1.TypedLocalObjectReference{
				Name:     snapshotName,
				Kind:     "VolumeSnapshot",
				APIGroup: func() *string { s := "snapshot.storage.k8s.io"; return &s }(),
			},
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
	// Create the PVC
	_, err := shootClientSet.CoreV1().PersistentVolumeClaims(namespace).Create(context.TODO(), pvc, metav1.CreateOptions{})
	if err != nil {
		fmt.Printf("Error creating PVC: %v\n", err)
		return err
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RestorePvcReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&snapshotv1beta1.RestorePvc{}).
		Complete(r)
}
