// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Subject defines the resource to bind to the placement resource.
type Subject struct {
	// APIGroup is the API group to which the kind belongs. Must be set to
	// "policy.open-cluster-management.io".
	//
	// +kubebuilder:validation:Enum=policy.open-cluster-management.io
	// +kubebuilder:validation:MinLength=1
	APIGroup string `json:"apiGroup"`

	// Kind is the kind of the object to bind to the placement resource. Must be set to either
	// "Policy" or "PolicySet".
	//
	// +kubebuilder:validation:Enum=Policy;PolicySet
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`

	// Name is the name of the policy or policy set to bind to the placement resource.
	//
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// PlacementSubject defines the placement resource that is being bound to the subjects defined in
// the placement binding.
type PlacementSubject struct {
	// APIGroup is the API group to which the kind belongs. Must be set to
	// "cluster.open-cluster-management.io" for Placement or "apps.open-cluster-management.io" for
	// PlacementRule (deprecated).
	//
	// +kubebuilder:validation:Enum=apps.open-cluster-management.io;cluster.open-cluster-management.io
	// +kubebuilder:validation:MinLength=1
	APIGroup string `json:"apiGroup"`

	// Kind is the kind of the placement resource. Must be set to either "Placement" or
	// "PlacementRule" (deprecated).
	//
	// +kubebuilder:validation:Enum=PlacementRule;Placement
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`

	// Name is the name of the placement resource being bound.
	//
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// BindingOverrides defines the overrides for the subjects.
type BindingOverrides struct {
	// RemediationAction overrides the policy remediationAction on target clusters. This parameter is
	// optional. If you set it, you must set it to "enforce".
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Enforce;enforce
	RemediationAction string `json:"remediationAction,omitempty"`
}

// SubFilter provides the ability to apply overrides to a subset of bound clusters when multiple
// placement bindings are used to bind a policy to placements. When set, only the overrides will be
// applied to the clusters bound by this placement binding but it will not be considered for placing
// the policy. This parameter is optional. If you set it, you must set it to "restricted".
type SubFilter string

const (
	Restricted SubFilter = "restricted"
)

// PlacementBindingStatus defines the observed state of the PlacementBinding resource.
type PlacementBindingStatus struct{}

// PlacementBinding is the schema for the placementbindings API. A PlacementBinding resource binds a
// managed cluster placement resource to a policy or policy set, along with configurable overrides.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=placementbindings,scope=Namespaced
// +kubebuilder:resource:path=placementbindings,shortName=pb
type PlacementBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	PlacementRef PlacementSubject `json:"placementRef"`

	// +kubebuilder:validation:MinItems=1
	Subjects []Subject `json:"subjects"`

	// +kubebuilder:validation:Optional
	BindingOverrides BindingOverrides `json:"bindingOverrides,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=restricted
	SubFilter SubFilter `json:"subFilter,omitempty"`

	Status PlacementBindingStatus `json:"status,omitempty"`
}

// PlacementBindingList contains a list of placement bindings.
//
// +kubebuilder:object:root=true
type PlacementBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PlacementBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PlacementBinding{}, &PlacementBindingList{})
}
