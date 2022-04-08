/*
Copyright 2022 The RamenDR authors.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DRClusterSpec defines the desired state of DRCluster
type DRClusterSpec struct {
	// Foo is an example field of DRCluster. Edit drcluster_types.go to remove/update
	Foo string `json:"foo,omitempty"`
}

// DRClusterStatus defines the observed state of DRCluster
type DRClusterStatus struct {
	Foo string `json:"foo,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// DRCluster is the Schema for the drclusters API
type DRCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DRClusterSpec   `json:"spec,omitempty"`
	Status DRClusterStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// DRClusterList contains a list of DRCluster
type DRClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DRCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DRCluster{}, &DRClusterList{})
}
