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
	StartTime() metav1.Time
	EndTime() metav1.Time
	Deallocate(context.Context, client.Writer, logr.Logger) error
}

type Requests interface {
	Count() int
	Get(i int) Request
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
	IncludedResources []string `json:"includedResources,omitempty"`

	//+optional
	ExcludedResources []string `json:"excludedResources,omitempty"`

	//+optional
	Hooks []HookSpec `json:"hooks,omitempty"`
}

type HookSpec struct {
	Name string `json:"name,omitempty"`

	Type string `json:"type,omitempty"`

	Command []string `json:"command,omitempty"`

	//+optional
	Timeout metav1.Duration `json:"timeout,omitempty"`

	//+optional
	Container string `json:"container,omitempty"`

	//+optional
	LabelSelector metav1.LabelSelector `json:"labelSelector,omitempty"`
}

func RequestProcessingErrorCreate(s string) RequestProcessingError { return RequestProcessingError{s} }
func (e RequestProcessingError) Error() string                     { return e.string }
func (RequestProcessingError) Is(err error) bool                   { return true }

type RequestsManager interface {
	ProtectsPath() string
	RecoversPath() string
	ProtectRequestNew() ProtectRequest
	RecoverRequestNew() RecoverRequest
	ProtectRequestCreate(
		c context.Context, w client.Writer, r client.Reader, l logr.Logger,
		s3Url string,
		s3BucketName string,
		s3RegionName string,
		s3KeyPrefix string,
		secretKeyRef *corev1.SecretKeySelector,
		sourceNamespaceName string,
		objectsSpec Spec,
		requestNamespaceName string,
		protectRequestName string,
		labels map[string]string,
	) (ProtectRequest, error)
	RecoverRequestCreate(
		c context.Context, w client.Writer, r client.Reader, l logr.Logger,
		s3Url string,
		s3BucketName string,
		s3RegionName string,
		s3KeyPrefix string,
		secretKeyRef *corev1.SecretKeySelector,
		sourceNamespaceName string,
		targetNamespaceName string,
		recoverSpec RecoverSpec,
		requestNamespaceName string,
		protectRequestName string,
		recoverRequestName string,
		labels map[string]string,
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
