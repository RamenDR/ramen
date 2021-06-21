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

// DRClusterPeersSpec defines the desired state of DRClusterPeers
type DRClusterPeersSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Members of the DRClusterPeers set
	ClusterNames []string `json:"clusterNames"`
}

// DRClusterPeersStatus defines the observed state of DRClusterPeers
// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
// Important: Run "make" to regenerate code after modifying this file
type DRClusterPeersStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// DRClusterPeers is the Schema for the drclusterpeers API
type DRClusterPeers struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DRClusterPeersSpec    `json:"spec,omitempty"`
	Status *DRClusterPeersStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DRClusterPeersList contains a list of DRClusterPeers
type DRClusterPeersList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DRClusterPeers `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DRClusterPeers{}, &DRClusterPeersList{})
}
