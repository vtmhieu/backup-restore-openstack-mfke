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

// SnapshotSpec defines the desired state of Snapshot
type SnapshotSpec struct {
	SnapshotSchedulerList []SnapshotScheduler `json:"snapshotSchedulerList,omitempty"`
	Create                Create              `json:"create,omitempty"`
	Delete                Delete              `json:"delete,omitempty"`
}

type Create struct {
	PvcName   string `json:"pvcName,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type Delete struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type SnapshotScheduler struct {
	Name            string                  `json:"name,omitempty"`
	PvcName         string                  `json:"pvcName,omitempty"`
	Namespace       string                  `json:"namespace,omitempty"`
	Schedules       []Scheduler             `json:"schedule,omitempty"`
	RetentionPolicy SnapshotRetentionPolicy `json:"retentionPolicy,omitempty"`
}

// SnapshotRetentionPolicy defines the policy for retaining snapshots.
type SnapshotRetentionPolicy struct {
	Type        string `json:"type,omitempty"`
	MaxDuration string `json:"maxDuration,omitempty"`
}

const (
	None string = "None"
	//StoreMaxSnapshots RetentionPolicyType = "StoreMaxSnapshots"
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

// CreateSnapshotStatus defines the observed state of CreateSnapshot
type SnapshotStatus struct {
	Conditions            []metav1.Condition  `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
	SnapshotSchedulerList []SnapshotScheduler `json:"snapshotSchedulerList,omitempty"`

	CreateSuccess bool `json:"createSuccess,omitempty"`
	DeleteSuccess bool `json:"deleteSuccess,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Snapshot is the Schema for the snapshots API
type Snapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SnapshotSpec   `json:"spec,omitempty"`
	Status SnapshotStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SnapshotList contains a list of CreateSnapshot
type SnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Snapshot `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Snapshot{}, &SnapshotList{})
}
