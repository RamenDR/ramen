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

// ClusterFenceState which will be either Unfenced, or Fenced or ManuallyFenced
// +kubebuilder:validation:Enum=Unfenced;Fenced;ManuallyFenced
type ClusterFenceState string

const (
	ClusterFenceStateUnfenced         = ClusterFenceState("Unfenced")
	ClusterFenceStateFenced           = ClusterFenceState("Fenced")
	ClusterFenceStateManuallyFenced   = ClusterFenceState("ManuallyFenced")
	ClusterFenceStateManuallyUnfenced = ClusterFenceState("ManuallyUnfenced")
)

type Region string

// DRClusterSpec defines the desired state of DRCluster
type DRClusterSpec struct {
	// CIDRs is a list of CIDR strings. An admin can use this field to indicate
	// the CIDRs that are used or could potentially be used for the nodes in
	// this managed cluster.  These will be used for the cluster fencing
	// operation for sync/Metro DR.
	CIDRs []string `json:"cidrs,omitempty"`

	// ClusterFence is a string that determines the desired fencing state of the cluster.
	ClusterFence ClusterFenceState `json:"clusterFence,omitempty"`

	// Region of a managed cluster determines it DR group.
	// All managed clusters in a region are considered to be in a sync group.
	Region Region `json:"region,omitempty"`

	// S3 profile name (in Ramen config) to use as a source to restore PV
	// related cluster state during recovery or relocate actions of applications
	// to this managed cluster;  hence, this S3 profile should be available to
	// successfully move the workload to this managed cluster.  For applications
	// that are active on this managed cluster, their PV related cluster state
	// is stored to S3 profiles of all other drclusters in the same
	// DRPolicy to enable recovery or relocate actions to those managed clusters.
	S3ProfileName string `json:"s3ProfileName"`
}

const (
	// DRCluster has been validated
	DRClusterValidated string = `Validated`

	// everything is clean. No fencing CRs present
	// in this cluster
	DRClusterConditionTypeClean = "Clean"

	// Fencing CR to fence off this cluster
	// has been created
	DRClusterConditionTypeFenced = "Fenced"
)

// DRClusterStatus defines the observed state of DRCluster
type DRClusterStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

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
