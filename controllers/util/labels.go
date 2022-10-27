// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

const (
	labelOwnerNamespaceName = "ramendr.openshift.io/owner-namespace-name"
	labelOwnerName          = "ramendr.openshift.io/owner-name"
)

func OwnerLabels(ownerNamespaceName, ownerName string) map[string]string {
	return map[string]string{
		labelOwnerNamespaceName: ownerNamespaceName,
		labelOwnerName:          ownerName,
	}
}

func OwnerNamespaceNameAndName(labels map[string]string) (string, string, bool) {
	ownerNamespaceName, ok1 := labels[labelOwnerNamespaceName]
	ownerName, ok2 := labels[labelOwnerName]

	return ownerNamespaceName, ownerName, ok1 && ok2
}
