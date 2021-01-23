/*
Copyright 2021.

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

// VolumeTakeoverControlType -- takeover control when desiredCluster is different from affinedCluster
type VolumeTakeoverControlType string

// VolumeTakeoverControlType definitions
const (
	// Force promote the volume in a WAN DR setting
	ForcePromote VolumeTakeoverControlType = "ForcePromote"
)

// ReplicatedApplicationSpec defines the desired state of ReplicatedApplication
type ReplicatedApplicationSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Name of the application to replicate
	ApplicationName string `json:"applicationName"`

	// Cluster ID in ClusterPeers that has best storage performance affinity for the application
	AffinedCluster string `json:"affinedCluster"`

	// Desired cluster that should takeover the application from current active cluster.
	// May be set either to:
	// - secondary cluster ID (to migrate application or takeover application in case of a disaster)
	// - nil (if application is not active in the affined cluster, takeback to affined cluster)
	// +optional
	DesiredCluster string `json:"desiredCluster,omitempty"`

	// Volume Takeover Control: ForcePromote
	// +optional
	VolumeTakeoverControl VolumeTakeoverControlType `json:"volumeTakeoverControl,omitempty"`

	// List of ClusterPeers
	// For Metro DR only: a single ClusterPeers
	// For WAN DR only: one or more ClusterPeers
	// For MetroDR and WAN DR: one Metro DR ClusterPeer and one or more WAN DR ClusterPeers.
	ClusterPeersList []string `json:"clusterPeersList"`

	// WAN DR RPO goal in seconds
	AsyncRPOGoalSeconds int64 `json:"asyncRPOGoalSeconds,omitempty"`
}

// ReplicatedApplicationStatus defines the observed state of ReplicatedApplication
type ReplicatedApplicationStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ReplicatedApplication is the Schema for the replicatedapplications API
type ReplicatedApplication struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReplicatedApplicationSpec   `json:"spec,omitempty"`
	Status ReplicatedApplicationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ReplicatedApplicationList contains a list of ReplicatedApplication
type ReplicatedApplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReplicatedApplication `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ReplicatedApplication{}, &ReplicatedApplicationList{})
}
