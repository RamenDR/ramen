// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DRClusterConfigSpec defines the desired state of DRClusterConfig
type DRClusterConfigSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of DRClusterConfig. Edit drclusterconfig_types.go to remove/update
	Foo string `json:"foo,omitempty"`
}

// DRClusterConfigStatus defines the observed state of DRClusterConfig
type DRClusterConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
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
