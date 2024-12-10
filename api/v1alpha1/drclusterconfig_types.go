// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DRClusterConfigSpec defines the desired state of DRClusterConfig
// It carries information regarding the cluster identity as known at the OCM hub cluster. It is also used to
// advertise required replication schedules on the cluster, if an equivalent DRPolicy resource is created for
// the same at the hub cluster.
// It is expected to be watched and used by storage providers that require meta information regarding the cluster
// and to prepare and manage required storage resources.
type DRClusterConfigSpec struct {
	// ReplicationSchedules desired from storage providers for replicating Persistent Volume data to a peer cluster.
	// Values are in the form <num><m,h,d>. Where <num> is a number, 'm' indicates minutes, 'h' means hours and
	// 'd' stands for days.
	// Typically used to generate VolumeReplicationClass resources with the desired schedules by storage
	// provider reconcilers
	ReplicationSchedules []string `json:"replicationSchedules,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="ClusterID is immutable"
	// ClusterID would carry the ManagedCluster identity from the ManagedCluster claim value for `id.k8s.io`
	ClusterID string `json:"clusterID,omitempty"`

	// TODO: PeerClusters []ClusterID; to decide if we really need this!
}

const (
	DRClusterConfigConfigurationProcessed string = "Processed"
	DRClusterConfigS3Reachable            string = "Reachable"
)

// DRClusterConfigStatus defines the observed state of DRClusterConfig
type DRClusterConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// DRClusterConfig is the Schema for the drclusterconfigs API
//
//nolint:maligned
type DRClusterConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DRClusterConfigSpec   `json:"spec,omitempty"`
	Status DRClusterConfigStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// DRClusterConfigList contains a list of DRClusterConfig
type DRClusterConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DRClusterConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DRClusterConfig{}, &DRClusterConfigList{})
}
