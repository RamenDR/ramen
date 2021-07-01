/*
Copyright 2021 The RamenDR authors.

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

// DRPolicySpec defines the desired state of DRPolicy
type DRPolicySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Members of the DRPolicy set
	ClusterNames []string `json:"clusterNames"`
}

// DRPolicyStatus defines the observed state of DRPolicy
// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
// Important: Run "make" to regenerate code after modifying this file
type DRPolicyStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// DRPolicy is the Schema for the drpolicies API
type DRPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DRPolicySpec    `json:"spec,omitempty"`
	Status *DRPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DRPolicyList contains a list of DRPolicy
type DRPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DRPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DRPolicy{}, &DRPolicyList{})
}
