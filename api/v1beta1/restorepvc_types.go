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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// RestorePvcSpec defines the desired state of RestorePvc
type RestorePvcSpec struct {
	// Define fields for RestorePVC spec
	RestorePvcName   string                              `json:"restorePvcName,omitempty"`
	SourcePvcName    string                              `json:"sourcePvcName,omitempty"`
	SnapshotName     string                              `json:"snapshotName,omitempty"`
	DesNamespace     string                              `json:"desNamespace,omitempty"`
	SourceNamespace  string                              `json:"sourceNamespace,omitempty"`
	AccessModes      []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`
	Storage          string                              `json:"storage,omitempty"`
	ConflictHandling string                              `json:"conflictHandling,omitempty"` // FailOnConflict || RollBack
}

// RestorePvcStatus defines the observed state of RestorePvc
type RestorePvcStatus struct {
	// Define fields for RestorePVC status
	Conditions     []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
	CreationStatus bool               `json:"creationStatus,omitempty"`

	PvcName            string                              `json:"pvcName,omitempty"`
	Resources          string                              `json:"resource,omitempty"`           // resources.requests.storage
	SourceSnapshotName string                              `json:"sourceSnapshotName,omitempty"` // dataSource.name
	Namespace          string                              `json:"namespace,omitempty"`
	VolumeName         string                              `json:"volumeName,omitempty"` // if status Pending -> return nil
	StorageClassName   string                              `json:"storageClass,omitempty"`
	AccessMode         []corev1.PersistentVolumeAccessMode `json:"accessMode,omitempty"`
	VolumeMode         string                              `json:"volumeMode,omitempty"`
	Status             string                              `json:"status,omitempty"` // Pending, Bounding
	CreationTime       string                              `json:"creationTime,omitempty"`
	ConflictHandling   string                              `json:"conflictHandling,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// RestorePvc is the Schema for the restorepvcs API
type RestorePvc struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RestorePvcSpec   `json:"spec,omitempty"`
	Status RestorePvcStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// RestorePvcList contains a list of RestorePvc
type RestorePvcList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RestorePvc `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RestorePvc{}, &RestorePvcList{})
}
