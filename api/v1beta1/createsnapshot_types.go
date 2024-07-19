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

// CreateSnapshotSpec defines the desired state of CreateSnapshot
type CreateSnapshotSpec struct {
	Create Create `json:"create,omitempty"`
	Delete Delete `json:"delete,omitempty"`
}

type Create struct {
	Name      string `json:"name,omitempty"`
	PvcName   string `json:"pvcName,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type Delete struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// CreateSnapshotStatus defines the observed state of CreateSnapshot
type CreateSnapshotStatus struct {
	Conditions    []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
	CreateSuccess bool               `json:"createSuccess,omitempty"`
	DeleteSuccess bool               `json:"deleteSuccess,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// CreateSnapshot is the Schema for the createsnapshots API
type CreateSnapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CreateSnapshotSpec   `json:"spec,omitempty"`
	Status CreateSnapshotStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// CreateSnapshotList contains a list of CreateSnapshot
type CreateSnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CreateSnapshot `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CreateSnapshot{}, &CreateSnapshotList{})
}
