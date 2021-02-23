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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// The clusterName is a custom resource that assigns a unique name to each
// kubernetes cluster that forms the larger multi-cluster DR peers. As it
// exists, kubernetes does not have any notion of a cluster ID, hence this
// custom resource serves as a substitute for the same. The multicluster-sig
// is also working towards defining a clusterID and clusterSets; in future,
// we may need to adapt/adopt the same.

// LocationType -- the locality type of a cluster
type LocationType string

// LocationType definitions
const (
	// LocalCluster implies that this is a local cluster
	LocalCluster LocationType = "LocalCluster"
	// MetroRemoteCluster implies that this cluster is not local and is part of a Metro DR replication
	MetroRemoteCluster LocationType = "MetroRemoteCluster"
	// WANRemoteCluster  implies that this cluster is not local and is part of a WAN DR replication
	WANRemoteCluster LocationType = "WANRemoteCluster" // WAN DR
)

// ExpectedConditionType -- present condition of the cluster, as known to the admin
type ExpectedConditionType string

// ExpectedConditionType definitions
const (
	// Cluster is expected to be recovering
	Recovering ExpectedConditionType = "recovering"
	// Cluster is expected to be up
	Up ExpectedConditionType = "up"
	// Cluster is expected to be down
	Down ExpectedConditionType = "down"
)

// ClusterIDSpec defines the desired state of ClusterID
type ClusterIDSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Name of this cluster.  Each cluster in a given ClusterPeers should have a unique name.
	Name string `json:"name"`

	// Location of this cluster: one of LocalCluster (local) or MetroRemoteCluster or WANRemoteCluster
	Location LocationType `json:"location"`
}

// ClusterIDStatus defines the observed state of ClusterID
type ClusterIDStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Fence status: fenced or unfenced
	FenceStatus string `json:"fenceStatus"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ClusterID is the Schema for the clusterids API
type ClusterID struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterIDSpec   `json:"spec,omitempty"`
	Status ClusterIDStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterIDList contains a list of ClusterID
type ClusterIDList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterID `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterID{}, &ClusterIDList{})
}
