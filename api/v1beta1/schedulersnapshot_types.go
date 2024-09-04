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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SchedulerSnapshotSpec defines the desired state of SchedulerSnapshot
type SchedulerSnapshotSpec struct {
	Enabled           bool              `json:"enabled,omitempty"`
	SnapshotScheduler SnapshotScheduler `json:"snapshotScheduler,omitempty"`
	// config backup scheduler -> in future
}

type SnapshotScheduler struct {
	PvcSnapshotClass    []PvcSnapshotClass              `json:"pvcSnapshotClass,omitempty"`
	ConfigSnapshotClass CreateKubeSnapshotBackupTargets `json:"configSnapshotClass,omitempty"`
	Schedules           []Scheduler                     `json:"schedule,omitempty"`
	RetentionPolicy     SnapshotRetentionPolicy         `json:"retentionPolicy,omitempty"`
}

// SnapshotRetentionPolicy defines the policy for retaining snapshots.
type SnapshotRetentionPolicy struct {
	TimeUnits string `json:"timeUnits,omitempty"` // minutes, hour, day
	Max       int    `json:"max,omitempty"`
}

type PvcSnapshotClass struct {
	PvcName   string `json:"pvcName,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

const (
	None                string = "None"
	StoreWithinDuration string = "StoreWithinDuration"

	OneHour     string = "OneHour"
	SevenDays   string = "SevenDays"
	FifteenDays string = "FifteenDays"
	OneMonth    string = "OneMonth"
)

type Scheduler struct {
	Start    string `json:"start,omitempty"`
	Location string `json:"location,omitempty"`
}

// SchedulerSnapshotStatus defines the observed state of SchedulerSnapshot
type SchedulerSnapshotStatus struct {
	Conditions        []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
	SnapshotScheduler SnapshotScheduler  `json:"snapshotScheduler,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="The age of the Scheduler"
// +kubebuilder:printcolumn:name="PvcName",type=string,JSONPath=`.status.snapshotScheduler.pvcName`
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=`.status.snapshotScheduler.namespace`,priority=1,description="The number of checks that passed"

// SchedulerSnapshot is the Schema for the schedulersnapshots API
type SchedulerSnapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SchedulerSnapshotSpec   `json:"spec,omitempty"`
	Status SchedulerSnapshotStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SchedulerSnapshotList contains a list of SchedulerSnapshot
type SchedulerSnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SchedulerSnapshot `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SchedulerSnapshot{}, &SchedulerSnapshotList{})
}
