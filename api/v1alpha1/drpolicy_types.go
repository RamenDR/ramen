// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DRPolicySpec defines the desired state of DRPolicy
// +kubebuilder:validation:XValidation:rule="has(oldSelf.replicationClassSelector) == has(self.replicationClassSelector)", message="replicationClassSelector is immutable"
// +kubebuilder:validation:XValidation:rule="has(oldSelf.volumeSnapshotClassSelector) == has(self.volumeSnapshotClassSelector)", message="volumeSnapshotClassSelector is immutable"
type DRPolicySpec struct {
	// scheduling Interval for replicating Persistent Volume
	// data to a peer cluster. Interval is typically in the
	// form <num><m,h,d>. Here <num> is a number, 'm' means
	// minutes, 'h' means hours and 'd' stands for days.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^(|\d+[mhd])$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="schedulingInterval is immutable"
	SchedulingInterval string `json:"schedulingInterval"`

	// Label selector to identify all the VolumeReplicationClasses.
	// This selector is assumed to be the same for all subscriptions that
	// need DR protection. It will be passed in to the VRG when it is created
	//+optional
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:={}
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="replicationClassSelector is immutable"
	ReplicationClassSelector metav1.LabelSelector `json:"replicationClassSelector"`

	// Label selector to identify all the VolumeSnapshotClasses.
	// This selector is assumed to be the same for all subscriptions that
	// need DR protection. It will be passed in to the VRG when it is created
	//+optional
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:={}
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="volumeSnapshotClassSelector is immutable"
	VolumeSnapshotClassSelector metav1.LabelSelector `json:"volumeSnapshotClassSelector"`

	// Label selector to identify the VolumeGroupSnapshotClass resources
	// that are scanned to select an appropriate VolumeGroupSnapshotClass
	// for the VolumeGroupSnapshot resource when using VolSync.
	//+optional
	VolumeGroupSnapshotClassSelector metav1.LabelSelector `json:"volumeGroupSnapshotClassSelector,omitempty"`

	// List of DRCluster resources that are governed by this policy
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="size(self) == 2", message="drClusters requires a list of 2 clusters"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="drClusters is immutable"
	DRClusters []string `json:"drClusters"`
}

// DRPolicyStatus defines the observed state of DRPolicy
type DRPolicyStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

const (
	DRPolicyValidated string = `Validated`
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// DRPolicy is the Schema for the drpolicies API
type DRPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DRPolicySpec   `json:"spec,omitempty"`
	Status DRPolicyStatus `json:"status,omitempty"`
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
