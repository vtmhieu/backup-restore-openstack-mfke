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
	"strconv"
	"strings"
	"time"

	gardenercorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	snapshotv1beta1 "github.com/vtmhieu/backup-restore-openstack-mfke.git/api/v1beta1"
)

const (
	kubeDumpToS3Image   = "registry.fke.fptcloud.com/762c8029-26d9-4bea-b461-989ee4d4890f/kube-dump-to-s3"
	kubeDumpToS3Version = "0.0.5"
)

const (
	createKubeSnapshotS3ConfigMapName = "createkubesnapshot-s3-configmap"
	createKubeSnapshotS3SecretsName   = "createkubesnapshot-s3-secret"
)

const (
	kubeSnapshotPodPollInterval = 15 * time.Second
)

// CreateKubeSnapshotReconciler reconciles a CreateKubeSnapshot object
type CreateKubeSnapshotReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=snapshot.mfke.io,resources=createkubesnapshots,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=snapshot.mfke.io,resources=createkubesnapshots/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=snapshot.mfke.io,resources=createkubesnapshots/finalizers,verbs=update

// Reconcile reconciles the CreateKubeSnapshot object. It will start a new
// backup if needed, otherwise reporting any status of the current backup.
//
// # State Diagram
//
// For the state diagram of this routine, see
// https://gitlab.fci.vn/xplat/fke/backup-restore-openstack-mfke/-/blob/e82714398aaf419fb0c52c80a291ea7d28e7064c/docs/createkubesnapshot.md
//
// # Reference
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.18.4/pkg/reconcile
func (r *CreateKubeSnapshotReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("CreateKubeSnapshotReconciler")
	logger.Info("Reconcile", "req", req)

	create := &snapshotv1beta1.CreateKubeSnapshot{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, create); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("error retrieving object from store: %w", err)
		}
		logger.V(1).Info("Object is gone, stop reconciling")
		return ctrl.Result{}, nil
	}

	logger = logger.WithValues(
		"namespace", create.Namespace,
		"currentRunCounter", create.Status.RunCounter,
		"desiredRunCounter", create.Spec.RunCounter)
	ctx = log.IntoContext(ctx, logger)

	// Ensure that the finalizer is set.
	// If it's not set, then we'll set it and requeue the reconciliation.
	if !controllerutil.ContainsFinalizer(create, SnapshotFinalizerName) {
		controllerutil.AddFinalizer(create, SnapshotFinalizerName)
		if err := r.Update(ctx, create); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("Finalizer added, requeueing")
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if the object is being deleted.  A zero DeletionTimestamp means we
	// can go ahead with our business.
	if create.ObjectMeta.DeletionTimestamp.IsZero() {
		logger.Info("Proceeding with backup")
		return r.doBackup(ctx, create)
	} else {
		logger.Info(
			"Cleaning up",
			"deletionTimestamp", create.DeletionTimestamp)
		return r.cleanup(ctx, create)
	}
}

// doBackup performs the backup process for the CreateKubeSnapshot object.
func (r *CreateKubeSnapshotReconciler) doBackup(ctx context.Context, create *snapshotv1beta1.CreateKubeSnapshot) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Update the status of the snapshot CRD.
	if err := r.updateStatus(ctx, create); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to update createkubesnapshot status: %w", err)
	}

	// Update our create object to reflect the changes and allow us to commit
	// it later.
	if err := r.Client.Update(ctx, create); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to update createkubesnapshot: %w", err)
	}

	// Clean up the finished pods.
	if err := r.cleanupPod(ctx, create, cleanupFinishedPods); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to clean up finished pods: %w", err)
	}

	if create.Spec.RunCounter > create.Status.RunCounter {
		logger.V(1).Info("Starting new backup")

		_, err := r.startNewBackup(ctx, create)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to start new backup: %w", err)
		}
	}

	if err := r.updateStatus(ctx, create); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to update createkubesnapshot status: %w", err)
	}

	// If there are still running pods or we still need to run more jobs, we'll
	// want to requeue the reconciliation to check on them later.
	if create.Status.OngoingRun != nil || create.Spec.RunCounter > create.Status.RunCounter {
		logger.V(1).Info(
			"Pod(s) still running, reconciling queued for some time",
			"interval", kubeSnapshotPodPollInterval)
		return ctrl.Result{RequeueAfter: kubeSnapshotPodPollInterval}, nil
	}

	logger.V(1).Info("Backup process completed")
	return ctrl.Result{}, nil
}

func (r *CreateKubeSnapshotReconciler) updateStatus(
	ctx context.Context,
	create *snapshotv1beta1.CreateKubeSnapshot) error {

	logger := log.FromContext(ctx)

	oldStatus := create.Status
	newStatus := oldStatus.DeepCopy()

	pod := buildKubeDumpPod(create, kubeDumpSettings{})
	if err := r.Client.Get(ctx, objectMetaToName(pod.ObjectMeta), pod); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("unable to get kube-dump pod: %w", err)
		}

		logger.V(1).Info(
			"Kube-dump pod not found, clearing ongoing run",
			"pod", pod.Name)
		newStatus.OngoingRun = nil
	} else {
		logger.V(1).Info(
			"Kube-dump pod found",
			"pod", pod.Name,
			"phase", pod.Status.Phase)

		podStatus := reportSnapshotPodStatus(pod)
		newStatus.OngoingRun = ptr.To(podStatus)

		switch podStatus.BackupStatus {
		case snapshotv1beta1.KubeSnapshotBackupSucceeded:
			newStatus.LastSuccessfulRun = ptr.To(podStatus)
		case snapshotv1beta1.KubeSnapshotBackupFailed:
			newStatus.LastFailedRun = ptr.To(podStatus)
		}
	}

	if newStatus.OngoingRun != nil {
		newStatus.BackupStatus = snapshotv1beta1.KubeSnapshotBackingUp
		newStatus.Message = "Backing up resources"
		newStatus.Reason = "BackingUp"
	} else {
		isFailing := true &&
			newStatus.LastFailedRun != nil &&
			newStatus.LastSuccessfulRun != nil &&
			newStatus.LastFailedRun.StartedAt.Time.After(newStatus.LastSuccessfulRun.StartedAt.Time)
		if isFailing {
			newStatus.BackupStatus = snapshotv1beta1.KubeSnapshotBackupFailed
			newStatus.Message = "The last backup failed"
			newStatus.Reason = "LastBackupFailed"
		} else {
			newStatus.BackupStatus = snapshotv1beta1.KubeSnapshotBackupSucceeded
			newStatus.Message = "The last backup succeeded"
			newStatus.Reason = "LastBackupSucceeded"
		}
	}

	previousRunSuccessful :=
		newStatus.LastSuccessfulRun != nil && (false ||
			oldStatus.LastSuccessfulRun == nil ||
			oldStatus.LastSuccessfulRun.StartedAt.Time.Before(newStatus.LastSuccessfulRun.StartedAt.Time))

	if previousRunSuccessful {
		// We got a new successful run, so we'll increment the run counter.
		newStatus.RunCounter++
	}

	create.Status = *newStatus
	logger.V(1).Info(
		"Updated status for createkubesnapshot",
		"backupStatus", create.Status.BackupStatus,
		"message", create.Status.Message,
		"reason", create.Status.Reason)

	if err := r.Client.Status().Update(ctx, create); err != nil {
		return fmt.Errorf("unable to commit createkubesnapshot status: %w", err)
	}

	return nil
}

// reportSnapshotPodStatus reports the status of the kube-dump pod into a
// [KubeSnapshotPodStatus].
func reportSnapshotPodStatus(kubeDumpPod *corev1.Pod) snapshotv1beta1.KubeSnapshotJobStatus {
	status := snapshotv1beta1.KubeSnapshotJobStatus{StartedAt: kubeDumpPod.CreationTimestamp}

	// Translate over the kube-dump pod's status to the snapshot CRD.
	switch kubeDumpPod.Status.Phase {
	case corev1.PodPending:
		status.BackupStatus = snapshotv1beta1.KubeSnapshotBackupInitializing
	case corev1.PodRunning:
		status.BackupStatus = snapshotv1beta1.KubeSnapshotBackingUp
	case corev1.PodSucceeded:
		status.BackupStatus = snapshotv1beta1.KubeSnapshotBackupSucceeded
	case corev1.PodFailed:
		status.BackupStatus = snapshotv1beta1.KubeSnapshotBackupFailed
	case corev1.PodUnknown:
		status.BackupStatus = snapshotv1beta1.KubeSnapshotBackupStatusUnknown
	}
	status.BackupPodMessage = kubeDumpPod.Status.Message
	status.BackupPodReason = kubeDumpPod.Status.Reason

	// Translate over the kube-dump container's status to the snapshot CRD.
	if len(kubeDumpPod.Status.ContainerStatuses) > 0 {
		containerStatus := kubeDumpPod.Status.ContainerStatuses[0]
		switch {
		case containerStatus.State.Waiting != nil:
			status.KubeDumpStatus = snapshotv1beta1.KubeDumpInitializing
			status.KubeDumpMessage = containerStatus.State.Waiting.Message
			status.KubeDumpReason = containerStatus.State.Waiting.Reason

		case containerStatus.State.Running != nil:
			status.KubeDumpStatus = snapshotv1beta1.KubeDumpRunning
			status.KubeDumpMessage = "kube-dump is running"
			status.KubeDumpReason = "Running"

		case containerStatus.State.Terminated != nil:
			if containerStatus.State.Terminated.ExitCode == 0 {
				status.KubeDumpStatus = snapshotv1beta1.KubeDumpSucceeded
			} else {
				status.KubeDumpStatus = snapshotv1beta1.KubeDumpFailed
			}
			status.KubeDumpMessage = containerStatus.State.Terminated.Message
			status.KubeDumpReason = containerStatus.State.Terminated.Reason
			status.FinishedAt = containerStatus.State.Terminated.FinishedAt
		}
	} else {
		status.KubeDumpStatus = snapshotv1beta1.KubeDumpStatusUnknown
	}

	return status
}

// startNewBackup starts a new kube-dump pod that will dump the resources in the
// target namespace. False is returned if it detects that a backup is already
// running.
//
// # Backup Mechanism
//
// To execute each backup, a new kube-dump pod is started in the internal
// namespace. This pod will use the kubeconfig secret from the shoot cluster to
// dump the resources onto S3.
//
// To easily orchestrate these pods, a job is created at the start when the
// feature is first enabled. This job initially does nothing, but once its
// completions count is incremented, it will automatically start up a new
// kube-dump pod in the background.
//
// In other words, a pod's lifetime is as long as a single kube-dump operation,
// but the job's lifetime is as long as the feature is enabled.
//
// Note that the job may be deleted at any time, for example by the TTL
// triggering. This shouldn't affect the backup process at all; this controller
// will simply recreate the job when it needs to start a new backup. It is worth
// pointing out that the controller will also work when a job is already present
// and will simply increment the completions count to start a new backup.
// For this reason, the TTL is solely for cleaning up old jobs and doesn't
// affect the backup process.
func (r *CreateKubeSnapshotReconciler) startNewBackup(
	ctx context.Context,
	create *snapshotv1beta1.CreateKubeSnapshot) (started bool, err error) {

	logger := log.FromContext(ctx)

	shootKubeconfig, err := GetSecretShootKubeconfigKey(ctx, r.Client, create.Namespace)
	if err != nil {
		return false, fmt.Errorf("unable to get shoot kubeconfig: %w (not a valid shoot?)", err)
	}

	// Figure out which node the target namespace's kube-apiserver is running on.
	// This will allow us to run the kube-dump pod on the same node to avoid
	// network latency.
	apiServerNode, err := nominateAPIServerNode(ctx, r.Client, create.Namespace)
	if err != nil {
		return false, fmt.Errorf("unable to nominate kube-apiserver node: %w", err)
	}

	logger.V(1).Info(
		"Nominated kube-apiserver node for kube-dump pod",
		"node", apiServerNode)

	pod := buildKubeDumpPod(create, kubeDumpSettings{
		NodeName:                  apiServerNode,
		ShootKubeconfigSecretName: shootKubeconfig.Name,
	})

	if err := r.Client.Create(ctx, pod); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			logger.Error(err,
				"Failed to create kube-dump pod",
				"namespace", pod.Namespace,
				"name", pod.Name)
			return false, fmt.Errorf("unable to create kube-dump pod: %w", err)
		}

		existingPod := &corev1.Pod{}
		if err := r.Client.Get(ctx, objectMetaToName(pod.ObjectMeta), existingPod); err != nil {
			logger.Error(err,
				"Failed to get existing kube-dump pod for updating during creation",
				"namespace", pod.Namespace,
				"name", pod.Name)
			return false, fmt.Errorf("unable to get existing kube-dump pod: %w", err)
		}

		// If the pod is still pending, then we may allow for comparing the pod
		// image to see if it needs updating.
		if existingPod.Status.Phase == corev1.PodPending && updatePodImages(pod, existingPod) {
			// The controller has a different spec than the existing pod, so we'll
			// update the pod to match the controller's spec. We will only do
			// this if the pod isn't already running.
			if err := r.Client.Update(ctx, existingPod); err != nil {
				logger.Error(err,
					"Failed to update kube-dump pod to match newer controller spec",
					"namespace", existingPod.Namespace,
					"name", existingPod.Name,
					"phase", existingPod.Status.Phase)
				return false, fmt.Errorf("unable to update kube-dump pod: %w", err)
			}

			logger.V(1).Info(
				"Updated kube-dump pod to match newer controller spec",
				"namespace", existingPod.Namespace,
				"name", existingPod.Name,
				"phase", existingPod.Status.Phase)
		} else {
			logger.V(1).Info("Backup pod already running, not spawning a new one")
		}

		return false, nil
	}

	logger.V(1).Info(
		"Created kube-dump pod",
		"namespace", pod.Namespace,
		"name", pod.Name)

	return true, nil
}

// cleanup removes the finalizer from the snapshot CRD and cleans up any resources
// that were created during the backup process. This will NOT remove the
// snapshots themselves, as those would've been in S3.
func (r *CreateKubeSnapshotReconciler) cleanup(ctx context.Context, create *snapshotv1beta1.CreateKubeSnapshot) (ctrl.Result, error) {
	// Delete all pods that's running the kube-dump process.
	if err := r.cleanupPod(ctx, create, cleanupAllPods); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to clean up pods: %w", err)
	}

	// Remove the finalizer from the snapshot CRD.
	controllerutil.RemoveFinalizer(create, SnapshotFinalizerName)

	// Update the snapshot CRD to reflect the changes.
	if err := r.Client.Update(ctx, create); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to update createkubesnapshot: %w", err)
	}

	// Job done.
	return ctrl.Result{}, nil
}

type cleanupPodEverything bool

const (
	cleanupAllPods      cleanupPodEverything = true
	cleanupFinishedPods cleanupPodEverything = false
)

func (r *CreateKubeSnapshotReconciler) cleanupPod(ctx context.Context, create *snapshotv1beta1.CreateKubeSnapshot, all cleanupPodEverything) error {
	logger := log.FromContext(ctx)

	// Delete all pods that's running the kube-dump process.
	pod := buildKubeDumpPod(create, kubeDumpSettings{})
	if err := r.Client.Get(ctx, objectMetaToName(pod.ObjectMeta), pod); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("unable to get kube-dump pod: %w", err)
		}
		logger.V(1).Info(
			"Kube-dump pod not found, nothing to clean up",
			"pod", pod.Name)
		return nil
	}

	if !all {
		switch pod.Status.Phase {
		case corev1.PodSucceeded, corev1.PodFailed:
			// Continue to delete the pod.
		default:
			logger.V(1).Info(
				"Kube-dump pod still running, not cleaning up",
				"pod", pod.Name,
				"phase", pod.Status.Phase)
			return nil
		}
	}

	if err := r.Client.Delete(ctx, pod); err != nil {
		logger.Error(err,
			"Failed to delete pod while cleaning up",
			"pod", pod.Name,
			"phase", pod.Status.Phase)
		return fmt.Errorf("unable to delete kube-dump pod: %w", err)
	} else {
		logger.V(1).Info(
			"Deleted pod",
			"pod", pod.Name,
			"phase", pod.Status.Phase)
	}

	return nil
}

// nominateAPIServerNode finds the node that the kube-apiserver pod is running
// on in the target namespace. For a list of kube-apiserver pods, it will return
// the node that most of the pods are running on.
func nominateAPIServerNode(ctx context.Context, c client.Client, targetNamespace string) (string, error) {
	kubeAPIServerPodList := &corev1.PodList{}

	if err := c.List(ctx, kubeAPIServerPodList,
		client.InNamespace(targetNamespace),
		client.MatchingLabels{
			"app":  "kubernetes",
			"role": "apiserver",
		},
	); err != nil {
		return "", fmt.Errorf("unable to list kube-apiserver pods in namespace %s: %w", targetNamespace, err)
	}

	nodes := make(map[string]int)
	for _, pod := range kubeAPIServerPodList.Items {
		if pod.Spec.NodeName != "" {
			nodes[pod.Spec.NodeName]++
		}
	}

	var maxNode string
	var maxCount int
	for node, count := range nodes {
		if count > maxCount {
			maxNode = node
			maxCount = count
		}
	}

	return maxNode, nil
}

type kubeDumpSettings struct {
	// NodeName is the name of the node that the kube-dump pod should run on.
	NodeName string
	// ShootKubeconfigSecretName is the name of the kubeconfig secret in the
	// internal namespace.
	ShootKubeconfigSecretName string
}

func buildKubeDumpPodMeta(create *snapshotv1beta1.CreateKubeSnapshot) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      "kube-dump-pod",
		Namespace: create.Namespace,
		Labels: map[string]string{
			LabelRole:                      "kube-dump",
			gardenercorev1beta1.LabelApp:   "backup-restore-openstack-mfke",
			gardenercorev1beta1.GardenRole: gardenercorev1beta1.GardenRoleSystemComponent,
			gardenercorev1beta1.LabelNetworkPolicyShootToAPIServer:     gardenercorev1beta1.LabelNetworkPolicyAllowed,
			gardenercorev1beta1.LabelNetworkPolicyShootToKubelet:       gardenercorev1beta1.LabelNetworkPolicyAllowed,
			gardenercorev1beta1.LabelNetworkPolicyToShootAPIServer:     gardenercorev1beta1.LabelNetworkPolicyAllowed,
			gardenercorev1beta1.LabelNetworkPolicyToAllShootAPIServers: gardenercorev1beta1.LabelNetworkPolicyAllowed,
			gardenercorev1beta1.LabelNetworkPolicyToSeedAPIServer:      gardenercorev1beta1.LabelNetworkPolicyAllowed,
			gardenercorev1beta1.LabelNetworkPolicyToDNS:                gardenercorev1beta1.LabelNetworkPolicyAllowed,
			gardenercorev1beta1.LabelNetworkPolicyToPrivateNetworks:    gardenercorev1beta1.LabelNetworkPolicyAllowed,
			gardenercorev1beta1.LabelNetworkPolicyToPublicNetworks:     gardenercorev1beta1.LabelNetworkPolicyAllowed,
			gardenercorev1beta1.LabelNetworkPolicyFromShootAPIServer:   gardenercorev1beta1.LabelNetworkPolicyAllowed,
		},
		Annotations: map[string]string{
			CreateKubeSnapshotRunCounterAnnotation: strconv.Itoa(int(create.Status.RunCounter + 1)),
		},
	}
}

// buildKubeDumpPod returns a pod that runs kube-dump to dump the resources in the target namespace
// using the kubeconfig secret in the shoot cluster.
func buildKubeDumpPod(create *snapshotv1beta1.CreateKubeSnapshot, settings kubeDumpSettings) *corev1.Pod {
	backup := create.Spec.BackupTargets

	kubeDumpArgs := []string{"--cluster", strconv.FormatBool(backup.IncludeClusterResources)}
	if backup.IncludeNamespaces != nil {
		kubeDumpArgs = append(kubeDumpArgs, "--namespaces", strings.Join(backup.IncludeNamespaces, ","))
	}

	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: buildKubeDumpPodMeta(create),
		Spec: corev1.PodSpec{
			NodeName:      settings.NodeName,
			RestartPolicy: corev1.RestartPolicyOnFailure,
			ImagePullSecrets: []corev1.LocalObjectReference{
				{Name: "regcred"},
			},
			Containers: []corev1.Container{
				{
					Name:            "kube-dump",
					Image:           kubeDumpToS3Image + ":" + kubeDumpToS3Version,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Args:            kubeDumpArgs,
					Env: []corev1.EnvVar{
						{Name: "DEBUG", Value: "true"},
						{Name: "TMPDIR", Value: "/tmp"}, // fix kube-dump archivate bug
						{Name: "KUBECONFIG", Value: "/run/secrets/k8s/kubeconfig"},
						{Name: "SECRETS_DIR", Value: "/run/secrets/s3"},
						{Name: "S3_PREFIX", Value: create.Namespace + "/" + create.Name},
						envFromConfigMap("S3_BUCKET", createKubeSnapshotS3ConfigMapName, "bucket"),
						envFromConfigMap("S3_REGION", createKubeSnapshotS3ConfigMapName, "region"),
						envFromConfigMap("S3_ENDPOINT", createKubeSnapshotS3ConfigMapName, "endpoint"),
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "kubeconfig", MountPath: "/run/secrets/k8s"},
						{Name: "s3-secrets", MountPath: "/run/secrets/s3"},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "kubeconfig",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: settings.ShootKubeconfigSecretName,
							Items: []corev1.KeyToPath{
								{
									Key:  "kubeconfig",
									Path: "kubeconfig",
								},
							},
						},
					},
				},
				{
					Name: "s3-secrets",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: createKubeSnapshotS3SecretsName,
							Items: []corev1.KeyToPath{
								{
									Key:  "id",
									Path: "s3_access_key",
								},
								{
									Key:  "secret",
									Path: "s3_secret_key",
								},
							},
						},
					},
				},
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *CreateKubeSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&snapshotv1beta1.CreateKubeSnapshot{}).
		Complete(r)
}
