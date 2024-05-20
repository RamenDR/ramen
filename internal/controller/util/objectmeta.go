// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// Return true if resource was marked for deletion.
func ResourceIsDeleted(obj client.Object) bool {
	return !obj.GetDeletionTimestamp().IsZero()
}

func ProtectedPVCNamespacedName(pvc ramen.ProtectedPVC) types.NamespacedName {
	return types.NamespacedName{
		Namespace: pvc.Namespace,
		Name:      pvc.Name,
	}
}
