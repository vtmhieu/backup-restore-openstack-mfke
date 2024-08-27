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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KubeSnapshotBackupStatus is the status of a backup operation.
type KubeSnapshotBackupStatus string

const (
	KubeSnapshotBackupStatusUnknown KubeSnapshotBackupStatus = "Unknown"
	KubeSnapshotBackupInitializing  KubeSnapshotBackupStatus = "Initializing"
	KubeSnapshotBackingUp           KubeSnapshotBackupStatus = "BackingUp"
	KubeSnapshotBackupFailed        KubeSnapshotBackupStatus = "Failed"
	KubeSnapshotBackupSucceeded     KubeSnapshotBackupStatus = "Succeeded"
)

// CreateKubeSnapshotSpec defines the desired state of CreateKubeSnapshot
type CreateKubeSnapshotSpec struct {
	// BackupTargets is the list of backup targets to use for the snapshot.
	BackupTargets CreateKubeSnapshotBackupTargets `json:"backupTargets"`
	// RunCounter is the counter for the run of the backup. This should be
	// incremented for each run of the backup.
	//
	// Note that the controller will not do anything until this counter is
	// higher than the counter in the status, so while you can set this to any
	// value, it will not have any effect until you properly increment it.
	RunCounter int32 `json:"runCounter"`
}

type CreateKubeSnapshotBackupTargets struct {
	// IncludeNamespaces is the list of namespaces to include in the snapshot.
	// If nil, all namespaces will be included. If empty, no namespaces will be
	// included.
	IncludeNamespaces []string `json:"includeNamespaces,omitempty"`
	// IncludeClusterResources, if true, will include cluster-wide resources in
	// the snapshot. Otherwise, only namespace-scoped resources will be included.
	IncludeClusterResources bool `json:"includeClusterResources,omitempty"`
}

// KubeDumpStatus is the status of the kube-dump container.
type KubeDumpStatus string

const (
	KubeDumpStatusUnknown KubeDumpStatus = "Unknown"
	KubeDumpInitializing  KubeDumpStatus = "Initializing"
	KubeDumpRunning       KubeDumpStatus = "Running"
	KubeDumpFailed        KubeDumpStatus = "Failed"
	KubeDumpSucceeded     KubeDumpStatus = "Succeeded"
)

// CreateKubeSnapshotStatus defines the observed state of CreateKubeSnapshot
type CreateKubeSnapshotStatus struct {
	// BackupStatus is the current status of the snapshot.
	BackupStatus KubeSnapshotBackupStatus `json:"backupStatus"`
	// Message is a message from the controller about the status of the backup.
	Message string `json:"message,omitempty"`
	// Reason is the reason for the backup status, if any.
	Reason string `json:"reason,omitempty"`

	// OngoingRun is the status of the current run of the backup.
	// It is nil if no runs are currently in progress.
	OngoingRun *KubeSnapshotJobStatus `json:"ongoingRun,omitempty"`
	// LastSuccessfulRun is the status of the last successful run of the backup.
	// It is nil if no successful runs have been made.
	LastSuccessfulRun *KubeSnapshotJobStatus `json:"lastSuccessfulRun,omitempty"`
	// LastFailedRun is the status of the last failed run of the backup.
	// It is nil if no failed runs have been made.
	LastFailedRun *KubeSnapshotJobStatus `json:"lastFailedRunStatus,omitempty"`

	// RunCounter is the counter for the run of the backup. This should be
	// incremented for each run of the backup.
	RunCounter int32 `json:"runCounter"`
}

// KubeSnapshotJobStatus defines the observed status of each Kube snapshot
// pod. When running a backup, there may be at least one instance of this pod
// status.
type KubeSnapshotJobStatus struct {
	// StartedAt is the time at which the job started.
	// Internally, this is the time that the kube-dump pod was created, not the
	// time that the kube-dump container has ended.
	StartedAt metav1.Time `json:"startedAt"`
	// FinishedAt is the time at which the job finished.
	// Internally, this is the time that the kube-dump container has ended.
	// This is nil if the job has not yet finished.
	FinishedAt metav1.Time `json:"finishedAt,omitempty"`

	// BackupStatus is the current status of the snapshot.
	BackupStatus KubeSnapshotBackupStatus `json:"backupStatus"`
	// BackupPodMessage is the message from the backup pod, if any.
	BackupPodMessage string `json:"backupPodMessage,omitempty"`
	// BackupPodReason is the reason for the backup pod status, if any.
	BackupPodReason string `json:"backupPodReason,omitempty"`

	// KubeDumpStatus is the status of the kube-dump container.
	KubeDumpStatus KubeDumpStatus `json:"kubeDumpStatus"`
	// KubeDumpRestarts is the number of restarts of the kube-dump container.
	// This count should be zero for a successful backup.
	KubeDumpRestarts int32 `json:"kubeDumpRestarts"`
	// KubeDumpMessage is the message from the kube-dump container, if any.
	KubeDumpMessage string `json:"kubeDumpMessage,omitempty"`
	// KubeDumpReason is the reason for the kube-dump container status, if any.
	KubeDumpReason string `json:"kubeDumpReason,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="backupStatus",type=string,JSONPath=`.status.backupStatus`
// +kubebuilder:printcolumn:name="message",type=string,JSONPath=`.status.message`
// +kubebuilder:printcolumn:name="reason",type=string,JSONPath=`.status.reason`
// +kubebuilder:printcolumn:name="runCounter",type=integer,JSONPath=`.status.runCounter`
// +kubebuilder:printcolumn:name="ongoingRun",type=string,JSONPath=`.status.ongoingRun.backupStatus`
// +kubebuilder:printcolumn:name="ongoingRunStartedAt",type=string,JSONPath=`.status.ongoingRun.startedAt`
// +kubebuilder:printcolumn:name="lastSuccessfulRun",type=string,JSONPath=`.status.lastSuccessfulRun.backupStatus`
// +kubebuilder:printcolumn:name="lastSuccessfulRunStartedAt",type=string,JSONPath=`.status.lastSuccessfulRun.startedAt`
// +kubebuilder:printcolumn:name="lastSuccessfulRunFinishedAt",type=string,JSONPath=`.status.lastSuccessfulRun.finishedAt`
// +kubebuilder:printcolumn:name="lastFailedRun",type=string,JSONPath=`.status.lastFailedRun.backupStatus`
// +kubebuilder:printcolumn:name="lastFailedRunStartedAt",type=string,JSONPath=`.status.lastFailedRun.startedAt`
// +kubebuilder:printcolumn:name="lastFailedRunFinishedAt",type=string,JSONPath=`.status.lastFailedRun.finishedAt`

// CreateKubeSnapshot is the Schema for the createkubesnapshots API
type CreateKubeSnapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CreateKubeSnapshotSpec   `json:"spec,omitempty"`
	Status CreateKubeSnapshotStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CreateKubeSnapshotList contains a list of CreateKubeSnapshot
type CreateKubeSnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CreateKubeSnapshot `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CreateKubeSnapshot{}, &CreateKubeSnapshotList{})
}
