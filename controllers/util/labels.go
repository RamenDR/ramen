// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	labelOwnerNamespaceName = "ramendr.openshift.io/owner-namespace-name"
	labelOwnerName          = "ramendr.openshift.io/owner-name"

	MModesLabel = "ramendr.openshift.io/maintenancemodes"
)

type Labels map[string]string

func ObjectLabelsSet(object metav1.Object, labels map[string]string) bool {
	return MapCopyF(labels, object.GetLabels, object.SetLabels)
}

func ObjectOwnerSet(object, owner metav1.Object) bool {
	return ObjectLabelsSet(object, OwnerLabels(owner.GetNamespace(), owner.GetName()))
}

func OwnerLabels(ownerNamespaceName, ownerName string) Labels {
	return Labels{
		labelOwnerNamespaceName: ownerNamespaceName,
		labelOwnerName:          ownerName,
	}
}

func OwnerNamespaceNameAndName(labels Labels) (string, string, bool) {
	ownerNamespaceName, ok1 := labels[labelOwnerNamespaceName]
	ownerName, ok2 := labels[labelOwnerName]

	return ownerNamespaceName, ownerName, ok1 && ok2
}

func OwnerNamespacedName(labels Labels) types.NamespacedName {
	ownerNamespaceName, ownerName, _ := OwnerNamespaceNameAndName(labels)

	return types.NamespacedName{
		Namespace: ownerNamespaceName,
		Name:      ownerName,
	}
}
