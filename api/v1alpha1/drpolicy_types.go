// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DRPolicySpec defines the desired state of DRPolicy
type DRPolicySpec struct {
	// Important: Run "make" to regenerate code after modifying this file

	// scheduling Interval for replicating Persistent Volume
	// data to a peer cluster. Interval is typically in the
	// form <num><m,h,d>. Here <num> is a number, 'm' means
	// minutes, 'h' means hours and 'd' stands for days.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^\d+[mhd]$`
	SchedulingInterval string `json:"schedulingInterval,omitempty"`

	// Label selector to identify all the VolumeReplicationClasses.
	// This selector is assumed to be the same for all subscriptions that
	// need DR protection. It will be passed in to the VRG when it is created
	//+optional
	ReplicationClassSelector metav1.LabelSelector `json:"replicationClassSelector,omitempty"`

	// Label selector to identify all the VolumeSnapshotClasses.
	// This selector is assumed to be the same for all subscriptions that
	// need DR protection. It will be passed in to the VRG when it is created
	//+optional
	VolumeSnapshotClassSelector metav1.LabelSelector `json:"volumeSnapshotClassSelector,omitempty"`

	// List of DRCluster resources that are governed by this policy
	DRClusters []string `json:"drClusters"`
}

// DRPolicyStatus defines the observed state of DRPolicy
// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
// Important: Run "make" to regenerate code after modifying this file
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
