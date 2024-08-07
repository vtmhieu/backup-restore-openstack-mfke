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

// PvcSpec defines the desired state of Pvc
type PvcSpec struct {
	// namespace of pvc
	Namespace string `json:"namespaceShoot,omitempty"`
}

// PvcStatus defines the observed state of Pvc
type PvcStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
	// total names of pvc in namespace
	PVCList []PvcDetail `json:"pvcList,omitempty"`
}

// PvcDetail defines the sum-up information of all PVC in shoot cluster
type PvcDetail struct {
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
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Pvc is the Schema for the pvcs API
type Pvc struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PvcSpec   `json:"spec,omitempty"`
	Status PvcStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PvcList contains a list of Pvc
type PvcList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Pvc `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Pvc{}, &PvcList{})
}
