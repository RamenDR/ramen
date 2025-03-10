// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package v1

import (
	"errors"
	"fmt"
	"strings"
	"time"

	depclient "github.com/stolostron/kubernetes-dependency-watches/client"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// +kubebuilder:validation:MinLength=1
type NonEmptyString string

// RemediationAction is the remediation of the policy. The parameter values are `enforce` and
// `inform`.
//
// +kubebuilder:validation:Enum=Inform;inform;Enforce;enforce
type RemediationAction string

const (
	Enforce RemediationAction = "Enforce"
	Inform  RemediationAction = "Inform"
)

func (ra RemediationAction) IsInform() bool {
	return strings.EqualFold(string(ra), string(Inform))
}

func (ra RemediationAction) IsEnforce() bool {
	return strings.EqualFold(string(ra), string(Enforce))
}

// CustomMessage configures the compliance messages emitted by the configuration policy, to use one
// of the specified Go templates based on the current compliance. The data passed to the templates
// include a `.DefaultMessage` string variable which matches the message that would be emitted if no
// custom template was defined, and a `.Policy` object variable which contains the full current
// state of the policy. If the policy is using Kubernetes API watches (default but can be configured
// with EvaluationInterval), and the object exists, then the full state of each related object will
// be available at `.Policy.status.relatedObjects[*].object`. Otherwise, only the identifier
// information will be available there.
type CustomMessage struct {
	// Compliant is the template used for the compliance message when the policy is compliant.
	Compliant string `json:"compliant,omitempty"`

	// NonCompliant is the template used for the compliance message when the policy is not compliant,
	// including when the status is unknown.
	NonCompliant string `json:"noncompliant,omitempty"`
}

// Severity is a user-defined severity for when an object is noncompliant with this configuration
// policy. The supported options are `low`, `medium`, `high`, and `critical`.
//
// +kubebuilder:validation:Enum=low;Low;medium;Medium;high;High;critical;Critical
type Severity string

// PruneObjectBehavior is used to remove objects that are managed by the policy upon either case: a
// change to the policy that causes an object to no longer be managed by the policy, or the deletion
// of the policy.
//
// +kubebuilder:validation:Enum=DeleteAll;DeleteIfCreated;None
type PruneObjectBehavior string

type Target struct {
	// Include is an array of filepath expressions to include objects by name.
	Include []NonEmptyString `json:"include,omitempty"`

	// Exclude is an array of filepath expressions to exclude objects by name.
	Exclude []NonEmptyString `json:"exclude,omitempty"`

	// MatchLabels is a map of {key,value} pairs matching objects by label.
	MatchLabels *map[string]string `json:"matchLabels,omitempty"`

	// MatchExpressions is an array of label selector requirements matching objects by label.
	MatchExpressions *[]metav1.LabelSelectorRequirement `json:"matchExpressions,omitempty"`
}

// Define String() so that the LabelSelector is dereferenced in the logs
func (t Target) String() string {
	fmtSelectorStr := "{include:%s,exclude:%s,matchLabels:%+v,matchExpressions:%+v}"
	if t.MatchLabels == nil && t.MatchExpressions == nil {
		return fmt.Sprintf(fmtSelectorStr, t.Include, t.Exclude, nil, nil)
	}

	if t.MatchLabels == nil {
		return fmt.Sprintf(fmtSelectorStr, t.Include, t.Exclude, nil, *t.MatchExpressions)
	}

	if t.MatchExpressions == nil {
		return fmt.Sprintf(fmtSelectorStr, t.Include, t.Exclude, *t.MatchLabels, nil)
	}

	return fmt.Sprintf(fmtSelectorStr, t.Include, t.Exclude, *t.MatchLabels, *t.MatchExpressions)
}

// EvaluationInterval configures the minimum elapsed time before a configuration policy is
// reevaluated. The default value is `watch` to leverage Kubernetes API watches instead of polling the Kubernetes API
// server. If the policy spec is changed or if the list of namespaces selected by the policy changes, the policy might
// be evaluated regardless of the settings here.
type EvaluationInterval struct {
	// Compliant is the minimum elapsed time before a configuration policy is reevaluated when in the
	// compliant state. Set this to `never` to disable reevaluation when in the compliant state. The default value is
	// `watch`.
	//
	//+kubebuilder:validation:Pattern=`^(?:(?:(?:[0-9]+(?:.[0-9])?)(?:h|m|s|(?:ms)|(?:us)|(?:ns)))|never|watch)+$`
	Compliant string `json:"compliant,omitempty"`
	// NonCompliant is the minimum elapsed time before a configuration policy is reevaluated when in the noncompliant
	// state. Set this to `never` to disable reevaluation when in the noncompliant state. The default value is `watch`.
	//
	//+kubebuilder:validation:Pattern=`^(?:(?:(?:[0-9]+(?:.[0-9])?)(?:h|m|s|(?:ms)|(?:us)|(?:ns)))|never|watch)+$`
	NonCompliant string `json:"noncompliant,omitempty"`
}

var (
	ErrIsNever = errors.New("the interval is set to never")
	ErrIsWatch = errors.New("the interval is set to watch")
)

// parseInterval converts the input string to a duration. ErrIsNever is returned when the string is set to `never`.
// ErrIsWatch is returned when the string is unset or set to `watch`.
func (e EvaluationInterval) parseInterval(interval string) (time.Duration, error) {
	if interval == "" || interval == "watch" {
		return 0, ErrIsWatch
	}

	if interval == "never" {
		return 0, ErrIsNever
	}

	parsedInterval, err := time.ParseDuration(interval)
	if err != nil {
		return 0, err
	}

	return parsedInterval, nil
}

func (e EvaluationInterval) IsWatchForCompliant() bool {
	return e.Compliant == "" || e.Compliant == "watch"
}

func (e EvaluationInterval) IsWatchForNonCompliant() bool {
	return e.NonCompliant == "" || e.NonCompliant == "watch"
}

// GetCompliantInterval converts the Compliant interval to a duration. ErrIsNever is returned when
// the string is set to `never`.
func (e EvaluationInterval) GetCompliantInterval() (time.Duration, error) {
	return e.parseInterval(e.Compliant)
}

// GetNonCompliantInterval converts the NonCompliant interval to a duration. ErrIsNever is returned
// when the string is set to `never`.
func (e EvaluationInterval) GetNonCompliantInterval() (time.Duration, error) {
	return e.parseInterval(e.NonCompliant)
}

type ComplianceType string

const (
	// MustNotHave is a ComplianceType to not match an object definition.
	MustNotHave ComplianceType = "MustNotHave"

	// MustHave is a ComplianceType to match an object definition as a subset of the whole object.
	MustHave ComplianceType = "MustHave"

	// MustOnlyHave is a ComplianceType to match an object definition exactly with the object.
	MustOnlyHave ComplianceType = "MustOnlyHave"
)

func (c ComplianceType) IsMustHave() bool {
	return strings.EqualFold(string(c), string(MustHave))
}

func (c ComplianceType) IsMustOnlyHave() bool {
	return strings.EqualFold(string(c), string(MustOnlyHave))
}

func (c ComplianceType) IsMustNotHave() bool {
	return strings.EqualFold(string(c), string(MustNotHave))
}

// +kubebuilder:validation:Enum=Log;InStatus;None
type RecordDiff string

const (
	RecordDiffLog      RecordDiff = "Log"
	RecordDiffInStatus RecordDiff = "InStatus"
	RecordDiffNone     RecordDiff = "None"
	// Censored is only used as an internal value to indicate a diff shouldn't be automatically generated.
	RecordDiffCensored RecordDiff = "Censored"
)

// +kubebuilder:validation:Enum=None;IfRequired;Always
type RecreateOption string

const (
	None       RecreateOption = "None"
	IfRequired RecreateOption = "IfRequired"
	Always     RecreateOption = "Always"
)

// ObjectTemplate describes the desired state of an object on the cluster.
type ObjectTemplate struct {
	// ComplianceType describes how objects on the cluster should be compared with the object definition
	// of the configuration policy. The supported options are `MustHave`, `MustOnlyHave`, or
	// `MustNotHave`.
	//
	// +kubebuilder:validation:Enum=MustHave;Musthave;musthave;MustOnlyHave;Mustonlyhave;mustonlyhave;MustNotHave;Mustnothave;mustnothave
	ComplianceType ComplianceType `json:"complianceType"`

	// MetadataComplianceType describes how the labels and annotations of objects on the cluster should
	// be compared with the object definition of the configuration policy. The supported options are
	// `MustHave` or `MustOnlyHave`. The default value is the value defined in `complianceType` for the
	// object template.
	//
	// +kubebuilder:validation:Enum=MustHave;Musthave;musthave;MustOnlyHave;Mustonlyhave;mustonlyhave
	MetadataComplianceType ComplianceType `json:"metadataComplianceType,omitempty"`

	// RecreateOption describes when to delete and recreate an object when an update is required. When you set the
	// object to `IfRequired`, the policy recreates the object when updating an immutable field. When you set the
	// parameter to `Always`, the policy recreates the object on any update. When you set the `remediationAction` to
	// `inform`, the parameter value, `recreateOption`, has no effect on the object. The `IfRequired` value has no
	// effect on clusters without dry-run update support. The default value is `None`.
	//+kubebuilder:default=None
	RecreateOption RecreateOption `json:"recreateOption,omitempty"`

	// ObjectDefinition defines required fields to be compared with objects on the cluster.
	//
	// +kubebuilder:pruning:PreserveUnknownFields
	ObjectDefinition runtime.RawExtension `json:"objectDefinition"`

	// RecordDiff specifies whether and where to log the difference between the object on the cluster
	// and the `objectDefinition` parameter in the policy. The supported options are `InStatus` to
	// record the difference in the policy status field, `Log` to log the difference in the
	// `config-policy-controller` pod, and `None` to not log the difference. The default value is
	// `None` for object kinds that include sensitive data such as `ConfigMap`, `OAuthAccessToken`,
	// `OAuthAuthorizeTokens`, `Route`, and `Secret`, or when a templated `objectDefinition`
	// references sensitive data. For all other kinds, the default value is `InStatus`.
	RecordDiff RecordDiff `json:"recordDiff,omitempty"`
}

// RecordDiffWithDefault parses the `objectDefinition` in the policy for the kind and returns the
// default `recordDiff` value depending on whether the kind contains sensitive data.
func (o *ObjectTemplate) RecordDiffWithDefault() RecordDiff {
	if o.RecordDiff != "" {
		return o.RecordDiff
	}

	_, gvk, err := unstructured.UnstructuredJSONScheme.Decode(o.ObjectDefinition.Raw, nil, nil)
	if err != nil {
		return o.RecordDiff
	}

	switch gvk.Group {
	case "":
		if gvk.Kind == "ConfigMap" || gvk.Kind == "Secret" {
			return RecordDiffCensored
		}
	case "oauth.openshift.io":
		if gvk.Kind == "OAuthAccessToken" || gvk.Kind == "OAuthAuthorizeTokens" {
			return RecordDiffCensored
		}
	case "route.openshift.io":
		if gvk.Kind == "Route" {
			return RecordDiffCensored
		}
	}

	return RecordDiffInStatus
}

// ConfigurationPolicySpec defines the desired configuration of objects on the cluster, along with
// how the controller should handle when the cluster doesn't match the configuration policy.
type ConfigurationPolicySpec struct {
	CustomMessage      CustomMessage      `json:"customMessage,omitempty"`
	Severity           Severity           `json:"severity,omitempty"`
	RemediationAction  RemediationAction  `json:"remediationAction"`
	EvaluationInterval EvaluationInterval `json:"evaluationInterval,omitempty"`
	// +kubebuilder:default:=None
	PruneObjectBehavior PruneObjectBehavior `json:"pruneObjectBehavior,omitempty"`

	// NamespaceSelector defines the list of namespaces to include or exclude for objects defined in
	// `spec["object-templates"]`. All selector rules are combined. If 'include' is not provided but
	// `matchLabels` and/or `matchExpressions` are, `include` will behave as if `['*']` were given. If
	// `matchExpressions` and `matchLabels` are both not provided, `include` must be provided to
	// retrieve namespaces.
	NamespaceSelector Target `json:"namespaceSelector,omitempty"`

	// The `object-templates` is an array of object configurations for the configuration policy to
	// check, create, modify, or delete objects on the cluster. Keys inside of the objectDefinition in
	// an object template may point to values that have Go templates. For more advanced Go templating
	// such as `range` loops and `if` conditionals, use `object-templates-raw`. Only one of
	// `object-templates` and `object-templates-raw` can be set in a configuration policy. For more on
	// the Go templates, see https://github.com/stolostron/go-template-utils/blob/main/README.md.
	ObjectTemplates []*ObjectTemplate `json:"object-templates,omitempty"`

	// The `object-templates-raw` is a string containing Go templates that must ultimately produce an
	// array of object configurations in YAML format to be used as `object-templates`. Only one of
	// `object-templates` and `object-templates-raw` can be set in a configuration policy. For more on
	// the Go templates, see https://github.com/stolostron/go-template-utils/blob/main/README.md.
	ObjectTemplatesRaw string `json:"object-templates-raw,omitempty"`
}

// ComplianceState reports the observed status from the definitions of the policy.
//
// +kubebuilder:validation:Enum=Compliant;Pending;NonCompliant;Terminating
type ComplianceState string

const (
	Compliant         ComplianceState = "Compliant"
	NonCompliant      ComplianceState = "NonCompliant"
	UnknownCompliancy ComplianceState = "UnknownCompliancy"
	Terminating       ComplianceState = "Terminating"
)

// Condition contains the details of an evaluation of an `object-template`.
type Condition struct {
	// Type is the type of condition. The supported options are `violation` or `notification`.
	Type string `json:"type"`

	// Status is an unused field. If set, it's set to `True`.
	Status corev1.ConditionStatus `json:"status,omitempty" protobuf:"bytes,12,rep,name=status"`

	// LastTransitionTime is the most recent time the condition transitioned to the current condition.
	//
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty" protobuf:"bytes,3,opt,name=lastTransitionTime"`

	// Reason is a brief summary for the condition.
	//
	// +optional
	Reason string `json:"reason,omitempty" protobuf:"bytes,4,opt,name=reason"`

	// Message is a human-readable message indicating details about the condition.
	//
	// +optional
	Message string `json:"message,omitempty" protobuf:"bytes,5,opt,name=message"`
}

type Validity struct { // UNUSED (attached to a field marked as deprecated)
	Valid  *bool  `json:"valid,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// TemplateStatus reports the compliance details from the definitions in an `object-template`.
type TemplateStatus struct {
	ComplianceState ComplianceState `json:"Compliant,omitempty"`

	// Conditions contains the details from the latest evaluation of the `object-template`.
	//
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []Condition `json:"conditions,omitempty"`

	// Deprecated
	Validity Validity `json:"Validity,omitempty"`
}

// ObjectMetadata contains the metadata for an object matched by the configuration policy.
type ObjectMetadata struct {
	// Name of the related object.
	Name string `json:"name,omitempty"`

	// Namespace of the related object.
	Namespace string `json:"namespace,omitempty"`
}

// ObjectResource contains details about an object matched by the configuration policy.
type ObjectResource struct {
	Metadata ObjectMetadata `json:"metadata,omitempty"`

	// Kind of the related object.
	Kind string `json:"kind,omitempty"`

	// API version of the related object.
	APIVersion string `json:"apiVersion,omitempty"`
}

// ObjectResourceFromObj mutates a Kubernetes object into an ObjectResource type to populate the
// policy status with related objects.
func ObjectResourceFromObj(obj client.Object) ObjectResource {
	name := obj.GetName()
	if name == "" {
		name = "-"
	}

	return ObjectResource{
		Kind:       obj.GetObjectKind().GroupVersionKind().Kind,
		APIVersion: obj.GetObjectKind().GroupVersionKind().GroupVersion().String(),
		Metadata: ObjectMetadata{
			Name:      name,
			Namespace: obj.GetNamespace(),
		},
	}
}

// Properties are additional properties of the related object relevant to the configuration policy.
type ObjectProperties struct {
	// CreatedByPolicy reports whether the object was created by the configuration policy, which is
	// important when pruning is configured.
	CreatedByPolicy *bool `json:"createdByPolicy,omitempty"`

	// UID stores the object UID to help track object ownership for deletion when pruning is
	// configured.
	UID string `json:"uid,omitempty"`

	// Diff stores the difference between the `objectDefinition` in the policy and the object on the
	// cluster.
	Diff string `json:"diff,omitempty"`
}

// RelatedObject contains the details of an object matched by the policy.
type RelatedObject struct {
	Properties *ObjectProperties `json:"properties,omitempty"`

	// ObjectResource contains the identifying fields of the related object.
	Object ObjectResource `json:"object,omitempty"`

	// Compliant represents whether the related object is compliant with the definition of the policy.
	Compliant string `json:"compliant,omitempty"`

	// Reason is a human-readable message of why the related object has a particular compliance.
	Reason string `json:"reason,omitempty"`
}

// ConfigurationPolicyStatus is the observed status of the configuration policy from its object
// definitions.
type ConfigurationPolicyStatus struct {
	ComplianceState ComplianceState `json:"compliant,omitempty"`

	// CompliancyDetails is a list of statuses matching one-to-one with each of the items in the
	// `object-templates` array.
	CompliancyDetails []TemplateStatus `json:"compliancyDetails,omitempty"`

	// LastEvaluated is an ISO-8601 timestamp of the last time the policy was evaluated.
	LastEvaluated string `json:"lastEvaluated,omitempty"`

	// LastEvaluatedGeneration is the generation of the ConfigurationPolicy object when it was last
	// evaluated.
	LastEvaluatedGeneration int64 `json:"lastEvaluatedGeneration,omitempty"`

	// RelatedObjects is a list of objects processed by the configuration policy due to its
	// `object-templates`.
	RelatedObjects []RelatedObject `json:"relatedObjects,omitempty"`
}

func (c ConfigurationPolicy) ObjectIdentifier() depclient.ObjectIdentifier {
	return depclient.ObjectIdentifier{
		Group:     GroupVersion.Group,
		Version:   GroupVersion.Version,
		Kind:      "ConfigurationPolicy",
		Namespace: c.Namespace,
		Name:      c.Name,
	}
}

// ConfigurationPolicy is the schema for the configurationpolicies API. A configuration policy
// contains, in whole or in part, an object definition to compare with objects on the cluster. If
// the definition of the configuration policy doesn't match the objects on the cluster, a
// noncompliant status is displayed. Furthermore, if the RemediationAction is set to `enforce` and
// the name of the object is available, the configuration policy controller creates or updates the
// object to match in order to make the configuration policy compliant.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Compliance state",type="string",JSONPath=".status.compliant"
type ConfigurationPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   *ConfigurationPolicySpec  `json:"spec,omitempty"`
	Status ConfigurationPolicyStatus `json:"status,omitempty"`
}

// ConfigurationPolicyList contains a list of configuration policies.
//
// +kubebuilder:object:root=true
type ConfigurationPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConfigurationPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConfigurationPolicy{}, &ConfigurationPolicyList{})
}
