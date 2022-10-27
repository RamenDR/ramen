// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ObjectMetaEmbedded(objectMeta *metav1.ObjectMeta) metav1.ObjectMeta {
	// github.com/kubernetes-sigs/controller-tools/pull/557
	return metav1.ObjectMeta{
		Namespace:   objectMeta.Namespace,
		Name:        objectMeta.Name,
		Annotations: objectMeta.Annotations,
		Labels:      objectMeta.Labels,
		Finalizers:  objectMeta.Finalizers,
	}
}
