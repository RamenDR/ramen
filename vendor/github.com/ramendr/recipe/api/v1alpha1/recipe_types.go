// SPDX-FileCopyrightText: IBM Corp.
// SPDX-License-Identifier: Apache2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

const (
	BackupWorkflowName  string = "backup"
	RestoreWorkflowName string = "restore"

	// TODO
	// Do we want to add capture and recover workflows?
)

// RecipeSpec defines the desired state of Recipe
type RecipeSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Type of application the recipe is designed for. (AppType is not used yet. For now, we will
	// match the name of the app CR)
	AppType string `json:"appType"`
	// List of one or multiple groups
	//+listType=map
	//+listMapKey=name
	//+optional
	Groups []*Group `json:"groups"`
	// Volumes to protect from disaster
	//+optional
	Volumes *Group `json:"volumes"`
	// List of one or multiple hooks
	//+listType=map
	//+listMapKey=name
	Hooks []*Hook `json:"hooks,omitempty"`
	// Workflow is the sequence of actions to take
	//+optional
	Workflows []*Workflow `json:"workflows"`
}

// Groups defined in the recipe refine / narrow-down the scope of its parent groups defined in the
// Application CR. Recipe groups are always be associated to a parent group in Application CR -
// explicitly or implicitly. Recipe groups can be used in the context of backup and/or restore workflows
type Group struct {
	// Name of the group
	Name string `json:"name"`
	// Name of the parent group defined in the associated Application CR. Optional - If unspecified,
	// parent group is represented by the implicit default group of Application CR (implies the
	// Application CR does not specify groups explicitly).
	Parent string `json:"parent,omitempty"`
	// Used for groups solely used in restore workflows to refer to another group that is used in
	// backup workflows.
	BackupRef string `json:"backupRef,omitempty"`
	// Determines the type of group - volume data only, resources only
	// +kubebuilder:validation:Enum=volume;resource
	Type string `json:"type"`
	// List of resource types to include. If unspecified, all resource types are included.
	IncludedResourceTypes []string `json:"includedResourceTypes,omitempty"`
	// List of resource types to exclude
	ExcludedResourceTypes []string `json:"excludedResourceTypes,omitempty"`
	// Select items based on label
	//+optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`
	// If specified, resource's object name needs to match this expression. Valid for volume groups only.
	NameSelector string `json:"nameSelector,omitempty"`
	// Determines the resource type which the fields labelSelector and nameSelector apply to for selecting PVCs. Default selection is pvc. Valid for volume groups only.
	// +kubebuilder:validation:Enum=pvc;pod;deployment;statefulset
	// +kubebuilder:validation:Optional
	SelectResource string `json:"selectResource,omitempty"`
	// Whether to include any cluster-scoped resources. If nil or true, cluster-scoped resources are
	// included if they are associated with the included namespace-scoped resources
	IncludeClusterResources *bool `json:"includeClusterResources,omitempty"`
	// Selects namespaces by label
	IncludedNamespacesByLabel *metav1.LabelSelector `json:"includedNamespacesByLabel,omitempty"`
	// List of namespaces to include.
	//+optional
	IncludedNamespaces []string `json:"includedNamespaces,omitempty"`
	// List of namespace to exclude
	ExcludedNamespaces []string `json:"excludedNamespaces,omitempty"`
	// RestoreStatus restores status if set to all the includedResources specified. Specify '*' to restore all statuses for all the CRs
	RestoreStatus *GroupRestoreStatus `json:"restoreStatus,omitempty"`
	// Defaults to true, if set to false, a failure is not necessarily handled as fatal
	Essential *bool `json:"essential,omitempty"`
	// Whether to overwrite resources during restore. Default to false.
	RestoreOverwriteResources *bool `json:"restoreOverwriteResources,omitempty"`
}

// GroupRestoreStatus is within resource groups which instructs velero to restore status for specified resources types, * would mean all
type GroupRestoreStatus struct {
	// List of resource types to include. If unspecified, all resource types are included.
	IncludedResources []string `json:"includedResources,omitempty"`
	// List of resource types to exclude.
	ExcludedResources []string `json:"excludedResources,omitempty"`
}

// Workflow is the sequence of actions to take
type Workflow struct {
	// Name of recipe. Names "backup" and "restore" are reserved and implicitly used by default for
	// backup or restore respectively
	Name string `json:"name"`
	// List of the names of groups or hooks, in the order in which they should be executed
	// Format: <group|hook>: <group or hook name>[/<hook op>]
	Sequence []map[string]string `json:"sequence"`
	// Implies behaviour in case of failure: any-error (default), essential-error, full-error
	// +kubebuilder:validation:Enum=any-error;essential-error;full-error
	// +kubebuilder:default=any-error
	FailOn string `json:"failOn,omitempty"`
}

// Hooks are actions to take during recipe processing
type Hook struct {
	// Hook name, unique within the Recipe CR
	Name string `json:"name"`
	// Namespace
	Namespace string `json:"namespace"`
	// Hook type
	// +kubebuilder:validation:Enum=exec;scale;check
	Type string `json:"type"`
	// Resource type to that a hook applies to
	SelectResource string `json:"selectResource,omitempty"`
	// If specified, resource object needs to match this label selector
	//+optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`
	// If specified, resource's object name needs to match this expression
	NameSelector string `json:"nameSelector,omitempty"`
	// Boolean flag that indicates whether to execute command on a single pod or on all pods that
	// match the selector
	SinglePodOnly bool `json:"singlePodOnly,omitempty"`
	// Default behavior in case of failing operations (custom or built-in ops). Defaults to Fail.
	// +kubebuilder:validation:Enum=fail;continue
	// +kubebuilder:default=fail
	OnError string `json:"onError,omitempty"`
	// Default timeout in seconds applied to custom and built-in operations. If not specified, equals to 30s.
	Timeout int `json:"timeout,omitempty"`
	// Set of operations that the hook can be invoked for
	//+listType=map
	//+listMapKey=name
	Ops []*Operation `json:"ops,omitempty"`
	// Set of checks that the hook can apply
	//+listType=map
	//+listMapKey=name
	Chks []*Check `json:"chks,omitempty"`
	// Defaults to true, if set to false, a failure is not necessarily handled as fatal
	Essential *bool `json:"essential,omitempty"`
}

// Operation to be invoked by the hook
type Operation struct {
	// Name of the operation. Needs to be unique within the hook
	Name string `json:"name"`
	// The container where the command should be executed
	Container string `json:"container,omitempty"`
	// The command to execute
	//+kubebuilder:validation:MinLength=1
	Command string `json:"command"`
	// How to handle command returning with non-zero exit code. Defaults to Fail.
	OnError string `json:"onError,omitempty"`
	// How long to wait for the command to execute, in seconds
	Timeout int `json:"timeout,omitempty"`
	// Name of another operation that reverts the effect of this operation (e.g. quiesce vs. unquiesce)
	InverseOp string `json:"inverseOp,omitempty"`
}

// Operation to be invoked by the hook
type Check struct {
	// Name of the check. Needs to be unique within the hook
	Name string `json:"name"`
	// The condition to check for
	Condition string `json:"condition,omitempty"`
	// How to handle when check does not become true. Defaults to Fail.
	OnError string `json:"onError,omitempty"`
	// How long to wait for the check to execute, in seconds
	Timeout int `json:"timeout,omitempty"`
}

// RecipeStatus defines the observed state of Recipe
type RecipeStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Recipe is the Schema for the recipes API
type Recipe struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RecipeSpec   `json:"spec,omitempty"`
	Status RecipeStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// RecipeList contains a list of Recipe
type RecipeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Recipe `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Recipe{}, &RecipeList{})
}
