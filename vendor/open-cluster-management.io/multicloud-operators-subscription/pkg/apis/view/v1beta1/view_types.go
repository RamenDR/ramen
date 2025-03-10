package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ManagedClusterView is the view of resources on a managed cluster
type ManagedClusterView struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired configuration of a view
	// +optional
	Spec ViewSpec `json:"spec,omitempty"`

	// Status describes current status of a view
	// +optional
	Status ViewStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ManagedClusterViewList is a list of all the ManagedClusterView
type ManagedClusterViewList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard list metadata.
	// More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#types-kinds
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// List of ManagedClusterView objects.
	Items []ManagedClusterView `json:"items"`
}

// ViewSpec defines the desired configuration of a view
type ViewSpec struct {
	// Scope is the scope of the view on a cluster
	Scope ViewScope `json:"scope,omitempty"`
}

// ViewStatus returns the status of the view
type ViewStatus struct {
	// Conditions represents the conditions of this resource on managed cluster
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Result references the related result of the view
	// +nullable
	// +optional
	// +kubebuilder:validation:EmbeddedResource
	// +kubebuilder:pruning:PreserveUnknownFields
	Result runtime.RawExtension `json:"result,omitempty"`
}

// ViewScope represents the scope of resources to be viewed
type ViewScope struct {
	// Group is the api group of the resources
	Group string `json:"apiGroup,omitempty"`

	// Version is the version of the subject
	// +optional
	Version string `json:"version,omitempty"`

	// Kind is the kind of the subject
	// +optional
	Kind string `json:"kind,omitempty"`

	// Resource is the resource type of the subject
	// +optional
	Resource string `json:"resource,omitempty"`

	// Name is the name of the subject
	// +optional
	Name string `json:"name,omitempty"`

	// Name is the name of the subject
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// UpdateIntervalSeconds is the interval to update view
	// +optional
	UpdateIntervalSeconds int32 `json:"updateIntervalSeconds,omitempty"`
}

// These are valid conditions of a cluster.
const (
	// ConditionViewProcessing means the view is processing.
	ConditionViewProcessing string = "Processing"
)

const (
	ReasonResourceNameInvalid string = "ResourceNameInvalid"
	ReasonResourceTypeInvalid string = "ResourceTypeInvalid"
	ReasonResourceGVKInvalid  string = "ResourceGVKInvalid"
	ReasonGetResourceFailed   string = "GetResourceFailed"
	ReasonGetResource         string = "GetResourceProcessing"
)

func init() {
	SchemeBuilder.Register(&ManagedClusterView{}, &ManagedClusterViewList{})
}
