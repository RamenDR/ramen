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
	Hooks []HookSpec `json:"hooks,omitempty"`
}

type HookSpec struct {
	Name string `json:"name,omitempty"`

	Type string `json:"type,omitempty"`

	Command string `json:"command,omitempty"`

	//+optional
	Timeout int `json:"timeout,omitempty"`

	//+optional
	Container *string `json:"container,omitempty"`

	//+optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`
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
