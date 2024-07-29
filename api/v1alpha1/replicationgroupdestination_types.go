// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReplicationGroupDestinationSpec defines the desired state of ReplicationGroupDestination
type ReplicationGroupDestinationSpec struct {
	// Label selector to identify the VolumeSnapshotClass resources
	// that are scanned to select an appropriate VolumeSnapshotClass
	// for the VolumeReplication resource when using VolSync.
	//+optional
	VolumeSnapshotClassSelector metav1.LabelSelector `json:"volumeSnapshotClassSelector,omitempty"`

	RDSpecs []VolSyncReplicationDestinationSpec `json:"rdSpecs,omitempty"`
}

// ReplicationGroupDestinationStatus defines the observed state of ReplicationGroupDestination
type ReplicationGroupDestinationStatus struct {
	// lastSyncTime is the time of the most recent successful synchronization.
	//+optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
	// lastSyncStartTime is the time the most recent synchronization started.
	//+optional
	LastSyncStartTime *metav1.Time `json:"lastSyncStartTime,omitempty"`
	// lastSyncDuration is the amount of time required to send the most recent
	// update.
	//+optional
	LastSyncDuration *metav1.Duration `json:"lastSyncDuration,omitempty"`
	// nextSyncTime is the time when the next volume synchronization is
	// scheduled to start (for schedule-based synchronization).
	//+optional
	NextSyncTime *metav1.Time `json:"nextSyncTime,omitempty"`
	// conditions represent the latest available observations of the
	// source's state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// latestImage in the object holding the most recent consistent replicated
	// image.
	//+optional
	LatestImages map[string]*corev1.TypedLocalObjectReference `json:"latestImage,omitempty"`
	// Created ReplicationDestinations by this ReplicationGroupDestination
	ReplicationDestinations []*corev1.ObjectReference `json:"replicationDestinations,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Last sync",type="string",format="date-time",JSONPath=`.status.lastSyncTime`
// +kubebuilder:printcolumn:name="Duration",type="string",JSONPath=`.status.lastSyncDuration`
// +kubebuilder:printcolumn:name="Last sync start",type="string",format="date-time",JSONPath=`.status.lastSyncStartTime`

// ReplicationGroupDestination is the Schema for the replicationgroupdestinations API
type ReplicationGroupDestination struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReplicationGroupDestinationSpec   `json:"spec,omitempty"`
	Status ReplicationGroupDestinationStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ReplicationGroupDestinationList contains a list of ReplicationGroupDestination
type ReplicationGroupDestinationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReplicationGroupDestination `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ReplicationGroupDestination{}, &ReplicationGroupDestinationList{})
}
