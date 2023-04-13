// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MMode defines a maintenance mode, that a storage backend may be requested to act on, based on the DR orchestration
// in progress for one or more workloads whose PVCs use the specific storage provisioner
// +kubebuilder:validation:Enum=Failover
type MMode string

// Supported maintenance modes
const (
	Failover = MMode("Failover")
)

// MaintenanceModeSpec defines the desired state of MaintenanceMode for a StorageProvisioner
type MaintenanceModeSpec struct {
	// StorageProvisioner indicates the type of the provisioner, and is matched with provisioner string present in the
	// StorageClass and/or VolumeReplicationClass for PVCs that are DR protected
	StorageProvisioner string `json:"storageProvisioner"`

	// TargetID indicates the storage or replication instance identifier for the StorageProvisioner that needs to handle
	// the requested maintenance modes. It is read using ramen specific labels on the StorageClass or
	// the VolumeReplicationClass as set by the storage provisioner
	TargetID string `json:"targetID,omitempty"`

	// Modes are the desired maintenance modes that the storage provisioner needs to act on
	Modes []MMode `json:"modes,omitempty"`
}

// MModeState defines the state of the system as per the desired spec, at a given generation of the spec (which is noted
// in status.observedGeneration)
// +kubebuilder:validation:Enum=Unknown;Error;Progressing;Completed
type MModeState string

// Valid values for MModeState
const (
	MModeStateUnknown     = MModeState("Unknown")
	MModeStateError       = MModeState("Error")
	MModeStateProgressing = MModeState("Progressing")
	MModeStateCompleted   = MModeState("Completed")
)

// MModeStatusConditionType defines an expected condition type
// +kubebuilder:validation:Enum=FailoverActivated
type MModeStatusConditionType string

// Valid MModeStatusConditionType types (condition types)
const (
	MModeConditionFailoverActivated = MModeStatusConditionType("FailoverActivated")
)

// MaintenanceModeStatus defines the observed state of MaintenanceMode
type MaintenanceModeStatus struct {
	State              MModeState         `json:"state,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// MaintenanceMode is the Schema for the maintenancemodes API
// TODO more details
type MaintenanceMode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MaintenanceModeSpec   `json:"spec,omitempty"`
	Status MaintenanceModeStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// MaintenanceModeList contains a list of MaintenanceMode
type MaintenanceModeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MaintenanceMode `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MaintenanceMode{}, &MaintenanceModeList{})
}
