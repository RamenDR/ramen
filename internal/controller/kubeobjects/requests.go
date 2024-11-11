// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package kubeobjects

import (
	"context"

	"github.com/go-logr/logr"
	velero "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type (
	ProtectRequest interface{ Request }
	RecoverRequest interface{ Request }
)

type Request interface {
	Object() client.Object
	Name() string
	StartTime() metav1.Time
	EndTime() metav1.Time
	Status(logr.Logger) error
	Deallocate(context.Context, client.Writer, logr.Logger) error
}

type Requests interface {
	Count() int
	Get(i int) Request
}

func RequestsMapKeyedByName(requestsStruct Requests) map[string]Request {
	requests := make(map[string]Request, requestsStruct.Count())

	for i := 0; i < requestsStruct.Count(); i++ {
		request := requestsStruct.Get(i)
		requests[request.Name()] = request
	}

	return requests
}

type RequestProcessingError struct{ string }

type CaptureSpec struct {
	//+optional
	Name string `json:"name,omitempty"`
	Spec `json:",inline"`
}

type RecoverSpec struct {
	//+optional
	BackupName string `json:"backupName,omitempty"`
	Spec       `json:",inline"`
	//+optional
	NamespaceMapping map[string]string `json:"namespaceMapping,omitempty"`
	//+optional
	RestoreStatus *velero.RestoreStatusSpec `json:"restoreStatus,omitempty"`
	//+optional
	ExistingResourcePolicy velero.PolicyType `json:"existingResourcePolicy,omitempty"`
}

type Spec struct {
	KubeResourcesSpec `json:",inline"`
	//+optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`

	//+optional
	OrLabelSelectors []*metav1.LabelSelector `json:"orLabelSelectors,omitempty"`

	//+optional
	IncludeClusterResources *bool `json:"includeClusterResources,omitempty"`
}

type KubeResourcesSpec struct {
	//+optional
	IncludedNamespaces []string `json:"includedNamespaces,omitempty"`
	//+optional
	IncludedResources []string `json:"includedResources,omitempty"`

	//+optional
	ExcludedResources []string `json:"excludedResources,omitempty"`

	//+optional
	Hook HookSpec `json:"hooks,omitempty"`

	//+optional
	IsHook bool `json:"isHook,omitempty"`
}

// HookSpec provides spec of either check or exec hook that needs to be executed
type HookSpec struct {
	Name           string                `json:"name"`
	Namespace      string                `json:"namespace"`
	Type           string                `json:"type"`
	SelectResource string                `json:"selectResource,omitempty"`
	LabelSelector  *metav1.LabelSelector `json:"labelSelector,omitempty"`
	NameSelector   string                `json:"nameSelector,omitempty"`
	SinglePodOnly  bool                  `json:"singlePodOnly,omitempty"`
	//+optional
	OnError string `json:"onError,omitempty"`

	//+optional
	Timeout   int   `json:"timeout,omitempty"`
	Essential *bool `json:"essential,omitempty"`

	Op Operation `json:"operation,omitempty"`

	Chk Check `json:"check,omitempty"`
}

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

type Operation struct {
	// Name of the operation. Needs to be unique within the hook
	Name string `json:"name"`
	// The container where the command should be executed
	Container string `json:"container,omitempty"`
	// The command to execute
	Command string `json:"command"`
	// How to handle command returning with non-zero exit code. Defaults to Fail.
	OnError string `json:"onError,omitempty"`
	// How long to wait for the command to execute, in seconds
	Timeout int `json:"timeout,omitempty"`
	// Name of another operation that reverts the effect of this operation (e.g. quiesce vs. unquiesce)
	InverseOp string `json:"inverseOp,omitempty"`
}

func RequestProcessingErrorCreate(s string) RequestProcessingError { return RequestProcessingError{s} }
func (e RequestProcessingError) Error() string                     { return e.string }

// Called by errors.Is() to match target.
func (RequestProcessingError) Is(target error) bool {
	_, ok := target.(RequestProcessingError)

	return ok
}

type RequestsManager interface {
	ProtectsPath() string
	RecoversPath() string
	ProtectRequestNew() ProtectRequest
	RecoverRequestNew() RecoverRequest
	ProtectRequestCreate(
		c context.Context, w client.Writer, l logr.Logger,
		s3Url string,
		s3BucketName string,
		s3RegionName string,
		s3KeyPrefix string,
		secretKeyRef *corev1.SecretKeySelector,
		caCertificates []byte,
		objectsSpec Spec,
		requestNamespaceName string,
		protectRequestName string,
		labels map[string]string,
		annotations map[string]string,
	) (ProtectRequest, error)
	RecoverRequestCreate(
		c context.Context, w client.Writer, l logr.Logger,
		s3Url string,
		s3BucketName string,
		s3RegionName string,
		s3KeyPrefix string,
		secretKeyRef *corev1.SecretKeySelector,
		caCertificates []byte,
		recoverSpec RecoverSpec,
		requestNamespaceName string,
		protectRequestName string,
		protectRequest ProtectRequest,
		recoverRequestName string,
		labels map[string]string,
		annotations map[string]string,
	) (RecoverRequest, error)
	ProtectRequestsGet(
		c context.Context, r client.Reader, requestNamespaceName string, labels map[string]string,
	) (Requests, error)
	RecoverRequestsGet(
		c context.Context, r client.Reader, requestNamespaceName string, labels map[string]string,
	) (Requests, error)
	ProtectRequestsDelete(c context.Context, w client.Writer, requestNamespaceName string, labels map[string]string) error
	RecoverRequestsDelete(c context.Context, w client.Writer, requestNamespaceName string, labels map[string]string) error
}
