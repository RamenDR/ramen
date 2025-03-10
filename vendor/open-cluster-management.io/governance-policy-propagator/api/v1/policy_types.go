// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// RemediationAction specifies the remediation of the policy. The parameter values are "enforce" and
// "inform".
//
// +kubebuilder:validation:Enum=Inform;inform;Enforce;enforce
type RemediationAction string

const (
	Enforce RemediationAction = "Enforce"
	Inform  RemediationAction = "Inform"
)

// PolicyTemplate is the definition of the policy engine resource to apply to the managed cluster,
// along with configurations on how it should be applied.
type PolicyTemplate struct {
	// A Kubernetes object defining the policy to apply to a managed cluster
	//
	// +kubebuilder:pruning:PreserveUnknownFields
	ObjectDefinition runtime.RawExtension `json:"objectDefinition"`

	// ExtraDependencies is additional PolicyDependencies that only apply to this policy template.
	// ExtraDependencies is a list of dependency objects detailed with extra considerations for
	// compliance that should be fulfilled before applying the policy template to the managed
	// clusters.
	ExtraDependencies []PolicyDependency `json:"extraDependencies,omitempty"`

	// IgnorePending is a boolean parameter to specify whether to ignore the "Pending" status of this
	// template when calculating the overall policy status. The default value is "false" to not ignore a
	// "Pending" status.
	IgnorePending bool `json:"ignorePending,omitempty"`
}

// ComplianceState reports the observed status resulting from the definitions of the policy.
//
// +kubebuilder:validation:Enum=Compliant;Pending;NonCompliant
type ComplianceState string

const (
	Compliant    ComplianceState = "Compliant"
	NonCompliant ComplianceState = "NonCompliant"
	Pending      ComplianceState = "Pending"
)

// Each PolicyDependency defines an object reference which must be in a certain compliance
// state before the policy should be created.
type PolicyDependency struct {
	metav1.TypeMeta `json:",inline"`

	// Name is the name of the object that the policy depends on.
	Name string `json:"name"`

	// Namespace is the namespace of the object that the policy depends on (optional).
	Namespace string `json:"namespace,omitempty"`

	// Compliance is the required ComplianceState of the object that the policy depends on, at the
	// following path, .status.compliant.
	Compliance ComplianceState `json:"compliance"`
}

type HubTemplateOptions struct {
	// ServiceAccountName is the name of a service account in the same namespace as the policy to use for all hub
	// template lookups. The service account must have list and watch permissions on any object the hub templates
	// look up. If not specified, lookups are restricted to namespaced objects in the same namespace as the policy and
	// to the `ManagedCluster` object associated with the propagated policy.
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
}

// PolicySpec defines the configurations of the policy engine resources to deliver to the managed
// clusters.
type PolicySpec struct {
	// Disabled is a boolean parameter you can use to enable and disable the policy. When disabled,
	// the policy is removed from managed clusters.
	Disabled bool `json:"disabled"`

	// CopyPolicyMetadata specifies whether the labels and annotations of a policy should be copied
	// when replicating the policy to a managed cluster. If set to "true", all of the labels and
	// annotations of the policy are copied to the replicated policy. If set to "false", only the
	// policy framework-specific policy labels and annotations are copied to the replicated policy.
	// This setting is useful if there is tracking for metadata that should only exist on the root
	// policy. It is recommended to set this to "false" when using Argo CD to deploy the policy
	// definition since Argo CD uses metadata for tracking that should not be replicated. The default
	// value is "true".
	//
	// +kubebuilder:validation:Optional
	CopyPolicyMetadata *bool `json:"copyPolicyMetadata,omitempty"`

	// RemediationAction specifies the remediation of the policy. The parameter values are "enforce"
	// and "inform". If specified, the value that is defined overrides any remediationAction parameter
	// defined in the child policies in the "policy-templates" section. Important: Not all policy
	// engine kinds support the enforce feature.
	RemediationAction RemediationAction `json:"remediationAction,omitempty"`

	// PolicyTemplates is a list of definitions of policy engine resources to apply to managed
	// clusters along with configurations on how it should be applied.
	PolicyTemplates []*PolicyTemplate `json:"policy-templates"`

	// PolicyDependencies is a list of dependency objects detailed with extra considerations for
	// compliance that should be fulfilled before applying the policies to the managed clusters.
	Dependencies []PolicyDependency `json:"dependencies,omitempty"`

	// HubTemplateOptions changes the default behavior of hub templates.
	HubTemplateOptions *HubTemplateOptions `json:"hubTemplateOptions,omitempty"`
}

// PlacementDecision is the cluster name returned by the placement resource.
type PlacementDecision struct {
	ClusterName      string `json:"clusterName,omitempty"`
	ClusterNamespace string `json:"clusterNamespace,omitempty"`
}

// Placement reports how and what managed cluster placement resources are attached to the policy.
type Placement struct {
	// PlacementBinding is the name of the PlacementBinding resource, from the
	// policies.open-cluster-management.io API group, that binds the placement resource to the policy.
	PlacementBinding string `json:"placementBinding,omitempty"`

	// PlacementRule (deprecated) is the name of the PlacementRule resource, from the
	// apps.open-cluster-management.io API group, that is bound to the policy.
	PlacementRule string `json:"placementRule,omitempty"`

	// Placement is the name of the Placement resource, from the cluster.open-cluster-management.io
	// API group, that is bound to the policy.
	Placement string `json:"placement,omitempty"`

	// Decisions is the list of managed clusters returned by the placement resource for this binding.
	Decisions []PlacementDecision `json:"decisions,omitempty"`

	// PolicySet is the name of the policy set containing this policy and bound to the placement. If
	// specified, then for this placement the policy is being propagated through this policy set
	// rather than the policy being bound directly to a placement and propagated individually.
	PolicySet string `json:"policySet,omitempty"`
}

// CompliancePerClusterStatus reports the name of a managed cluster and its compliance state for
// this policy.
type CompliancePerClusterStatus struct {
	ComplianceState  ComplianceState `json:"compliant,omitempty"`
	ClusterName      string          `json:"clustername,omitempty"`
	ClusterNamespace string          `json:"clusternamespace,omitempty"`
}

// DetailsPerTemplate reports the current compliance state and list of recent compliance messages
// for a given policy template.
type DetailsPerTemplate struct {
	// +kubebuilder:pruning:PreserveUnknownFields
	TemplateMeta    metav1.ObjectMeta   `json:"templateMeta,omitempty"`
	ComplianceState ComplianceState     `json:"compliant,omitempty"`
	History         []ComplianceHistory `json:"history,omitempty"`
}

// ComplianceHistory reports a compliance message from a given time and event.
type ComplianceHistory struct {
	// LastTimestamp is the timestamp of the event that reported the message.
	LastTimestamp metav1.Time `json:"lastTimestamp,omitempty" protobuf:"bytes,7,opt,name=lastTimestamp"`

	// Message is the compliance message resulting from evaluating the policy resource.
	Message string `json:"message,omitempty" protobuf:"bytes,4,opt,name=message"`

	// EventName is the name of the event attached to the message.
	EventName string `json:"eventName,omitempty"`
}

// PolicyStatus reports the observed status of the policy resulting from its policy templates.
type PolicyStatus struct {
	// ROOT POLICY STATUS FIELDS

	// Placement is a list of managed cluster placement resources bound to the policy. This status
	// field is only used in the root policy on the hub cluster.
	Placement []*Placement `json:"placement,omitempty"`

	// Status is a list of managed clusters and the current compliance state of each one. This
	// status field is only used in the root policy on the hub cluster.
	Status []*CompliancePerClusterStatus `json:"status,omitempty"`

	// REPLICATED POLICY STATUS FIELDS

	// ComplianceState reports the observed status resulting from the definitions of this policy. This
	// status field is only used in the replicated policy in the managed cluster namespace.
	ComplianceState ComplianceState `json:"compliant,omitempty"`

	// Details is the list of compliance details for each policy template definition. This status
	// field is only used in the replicated policy in the managed cluster namespace.
	Details []*DetailsPerTemplate `json:"details,omitempty"`
}

// Policy is the schema for the policies API. Policy wraps other policy engine resources in its
// "policy-templates" array in order to deliver the resources to managed clusters.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=policies,scope=Namespaced
// +kubebuilder:resource:path=policies,shortName=plc
// +kubebuilder:printcolumn:name="Remediation action",type="string",JSONPath=".spec.remediationAction"
// +kubebuilder:printcolumn:name="Compliance state",type="string",JSONPath=".status.compliant"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type Policy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   PolicySpec   `json:"spec"`
	Status PolicyStatus `json:"status,omitempty"`
}

// PolicyList contains a list of policies.
//
// +kubebuilder:object:root=true
type PolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Policy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Policy{}, &PolicyList{})
}
