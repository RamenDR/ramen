// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReplicationGroupSourceSpec defines the desired state of ReplicationGroupSource
type ReplicationGroupSourceSpec struct {
	Trigger *ReplicationSourceTriggerSpec `json:"trigger,omitempty"`

	// +required
	VolumeGroupSnapshotClassName string `json:"volumeGroupSnapshotClassName,omitempty"`

	// +required
	VolumeGroupSnapshotSource *metav1.LabelSelector `json:"volumeGroupSnapshotSource,omitempty"`
}

// ReplicationSourceTriggerSpec defines when a volume will be synchronized with
// the destination.
type ReplicationSourceTriggerSpec struct {
	// schedule is a cronspec (https://en.wikipedia.org/wiki/Cron#Overview) that
	// can be used to schedule replication to occur at regular, time-based
	// intervals.
	// nolint:lll
	//+kubebuilder:validation:Pattern=`^(@(annually|yearly|monthly|weekly|daily|hourly))|((((\d+,)*\d+|(\d+(\/|-)\d+)|\*(\/\d+)?)\s?){5})$`
	//+optional
	Schedule *string `json:"schedule,omitempty"`
	// manual is a string value that schedules a manual trigger.
	// Once a sync completes then status.lastManualSync is set to the same string value.
	// A consumer of a manual trigger should set spec.trigger.manual to a known value
	// and then wait for lastManualSync to be updated by the operator to the same value,
	// which means that the manual trigger will then pause and wait for further
	// updates to the trigger.
	//+optional
	Manual string `json:"manual,omitempty"`
}

// ReplicationGroupSourceStatus defines the observed state of ReplicationGroupSource
type ReplicationGroupSourceStatus struct {
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
	// lastManualSync is set to the last spec.trigger.manual when the manual sync is done.
	//+optional
	LastManualSync string `json:"lastManualSync,omitempty"`
	// conditions represent the latest available observations of the
	// source's state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// Created ReplicationSources by this ReplicationGroupSource
	ReplicationSources []*corev1.ObjectReference `json:"replicationSources,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Last sync",type="string",format="date-time",JSONPath=`.status.lastSyncTime`
// +kubebuilder:printcolumn:name="Duration",type="string",JSONPath=`.status.lastSyncDuration`
// +kubebuilder:printcolumn:name="Next sync",type="string",format="date-time",JSONPath=`.status.nextSyncTime`
// +kubebuilder:printcolumn:name="Source",type="string",JSONPath=`.spec.volumeGroupSnapshotSource`
// +kubebuilder:printcolumn:name="Last sync start",type="string",format="date-time",JSONPath=`.status.lastSyncStartTime`
// +kubebuilder:resource:shortName=rgs

// ReplicationGroupSource is the Schema for the replicationgroupsources API
type ReplicationGroupSource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReplicationGroupSourceSpec   `json:"spec,omitempty"`
	Status ReplicationGroupSourceStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ReplicationGroupSourceList contains a list of ReplicationGroupSource
type ReplicationGroupSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReplicationGroupSource `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ReplicationGroupSource{}, &ReplicationGroupSourceList{})
}
