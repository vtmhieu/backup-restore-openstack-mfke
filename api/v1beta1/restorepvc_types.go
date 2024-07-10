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

// RestorePvcSpec defines the desired state of RestorePvc
type RestorePvcSpec struct {
	// Define fields for RestorePVC spec
	SnapshotName string `json:"snapshotName"`
	Namespace    string `json:"namespace"`
}

// RestorePvcStatus defines the observed state of RestorePvc
type RestorePvcStatus struct {
	// Define fields for RestorePVC status
	RestoredPVCName string `json:"restoredPvcName"`
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
