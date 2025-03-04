// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterFenceState which will be either Unfenced, Fenced, ManuallyFenced or ManuallyUnfenced
// +kubebuilder:validation:Enum=Unfenced;Fenced;ManuallyFenced;ManuallyUnfenced
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
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="region is immutable"
	Region Region `json:"region,omitempty"`

	// S3 profile name (in Ramen config) to use as a source to restore PV
	// related cluster state during recovery or relocate actions of applications
	// to this managed cluster;  hence, this S3 profile should be available to
	// successfully move the workload to this managed cluster.  For applications
	// that are active on this managed cluster, their PV related cluster state
	// is stored to S3 profiles of all other drclusters in the same
	// DRPolicy to enable recovery or relocate actions to those managed clusters.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="s3ProfileName is immutable"
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

type DRClusterPhase string

// These are the valid values for DRState
const (
	// Available, state recorded in the DRCluster status to indicate that this
	// resource is available. Usually done when there is no fencing state
	// provided in the spec and DRCluster just reconciles to validate itself.
	Available = DRClusterPhase("Available")

	// Starting, state recorded in the DRCluster status to indicate that this
	// is the start of the reconciler.
	Starting = DRClusterPhase("Starting")

	// Fencing, state recorded in the DRCluster status to indicate that
	// fencing is in progress. Fencing means selecting the
	// peer cluster and creating a NetworkFence MW for it and waiting for MW
	// to be applied in the managed cluster
	Fencing = DRClusterPhase("Fencing")

	// Fenced, this is the state that will be recorded in the DRCluster status
	// when fencing has been performed successfully
	Fenced = DRClusterPhase("Fenced")

	// Unfencing, state recorded in the DRCluster status to indicate that
	// unfencing is in progress. Unfencing means selecting the
	// peer cluster and creating/updating a NetworkFence MW for it and waiting for MW
	// to be applied in the managed cluster
	Unfencing = DRClusterPhase("Unfencing")

	// Unfenced, this is the state that will be recorded in the DRCluster status
	// when unfencing has been performed successfully
	Unfenced = DRClusterPhase("Unfenced")
)

type ClusterMaintenanceMode struct {
	// StorageProvisioner indicates the type of the provisioner
	StorageProvisioner string `json:"storageProvisioner"`

	// TargetID indicates the storage or replication instance identifier for the StorageProvisioner
	TargetID string `json:"targetID"`

	// State from MaintenanceMode resource created for the StorageProvisioner
	State MModeState `json:"state"`

	// Conditions from MaintenanceMode resource created for the StorageProvisioner
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// DRClusterStatus defines the observed state of DRCluster
type DRClusterStatus struct {
	Phase            DRClusterPhase           `json:"phase,omitempty"`
	Conditions       []metav1.Condition       `json:"conditions,omitempty"`
	MaintenanceModes []ClusterMaintenanceMode `json:"maintenanceModes,omitempty"`
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
