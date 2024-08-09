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
	PvcName      string `json:"pvcName,omitempty"`
	SnapshotName string `json:"snapshotName,omitempty"`
	Namespace    string `json:"namespace,omitempty"`
	SnapshotType string `json:"snapshotType,omitempty"`
}

// CreateSnapshotStatus defines the observed state of CreateSnapshot
type SnapshotStatus struct {
	Conditions              []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
	SourcePvcName           string             `json:"pvcName,omitempty"`
	SnapshotName            string             `json:"snapshotName,omitempty"`
	Namespace               string             `json:"namespace,omitempty"`
	CreationStatus          string             `json:"creationStatus,omitempty"`
	VolumeSnapshotClassName string             `json:"volumeSnapshotClassName,omitempty"`
	SnapshotContentName     string             `json:"snapshotContentName,omitempty"`
	CreationTime            string             `json:"creationTime,omitempty"`
	ReadyToUse              bool               `json:"readyToUse,omitempty"`
	RestoreSize             string             `json:"restoreSize,omitempty"`
	SnapshotType            string             `json:"snapshotType,omitempty"`
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
