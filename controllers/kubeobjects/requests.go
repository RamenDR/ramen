/*
Copyright 2022 The RamenDR authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kubeobjects

import (
	"context"

	"github.com/go-logr/logr"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
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

func RequestProcessingErrorCreate(s string) RequestProcessingError {
	return RequestProcessingError{s}
}

func (e RequestProcessingError) Error() string   { return e.string }
func (RequestProcessingError) Is(err error) bool { return true }

type RequestsManager interface {
	ProtectsPath() string
	RecoversPath() string
	ProtectRequestNew() ProtectRequest
	RecoverRequestNew() RecoverRequest
	ProtectRequestCreate(c context.Context, w client.Writer, r client.Reader, l logr.Logger,
		s3Url string,
		s3BucketName string,
		s3RegionName string,
		s3KeyPrefix string,
		secretKeyRef *corev1.SecretKeySelector,
		sourceNamespaceName string,
		objectsSpec ramen.KubeObjectsSpec,
		requestNamespaceName string,
		protectRequestName string,
		labels map[string]string,
	) (ProtectRequest, error)
	RecoverRequestCreate(c context.Context, w client.Writer, r client.Reader, l logr.Logger,
		s3Url string,
		s3BucketName string,
		s3RegionName string,
		s3KeyPrefix string,
		secretKeyRef *corev1.SecretKeySelector,
		sourceNamespaceName string,
		targetNamespaceName string,
		objectsSpec ramen.KubeObjectsSpec,
		requestNamespaceName string,
		protectRequestName string,
		recoverRequestName string,
		labels map[string]string,
	) (RecoverRequest, error)
	ProtectRequestsGet(c context.Context, r client.Reader, requestNamespaceName string, labels map[string]string,
		) (Requests, error)
	RecoverRequestsGet(c context.Context, r client.Reader, requestNamespaceName string, labels map[string]string,
		) (Requests, error)
	ProtectRequestsDelete(c context.Context, w client.Writer, requestNamespaceName string, labels map[string]string) error
	RecoverRequestsDelete(c context.Context, w client.Writer, requestNamespaceName string, labels map[string]string) error
}
