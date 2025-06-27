// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	LabelOwnerNamespaceName = "ramendr.openshift.io/owner-namespace-name"
	LabelOwnerName          = "ramendr.openshift.io/owner-name"

	MModesLabel                           = "ramendr.openshift.io/maintenancemodes"
	SClassLabel                           = "ramendr.openshift.io/storageclass"
	VSClassLabel                          = "ramendr.openshift.io/volumesnapshotclass"
	VGSClassLabel                         = "ramendr.openshift.io/volumegroupsnapshotclass"
	VRClassLabel                          = "ramendr.openshift.io/volumereplicationclass"
	VGRClassLabel                         = "ramendr.openshift.io/volumegroupreplicationclass"
	ExcludeFromVeleroBackup               = "velero.io/exclude-from-backup"
	VeleroKubevirtMetadataOnlyBackupLabel = "velero.kubevirt.io/metadataBackup"
)

type Labels map[string]string

func ObjectLabelsSet(object metav1.Object, labels map[string]string) bool {
	return ObjectLabelsDo(object, labels, MapCopyF[map[string]string, string, string])
}

func ObjectLabelsDelete(object metav1.Object, labels map[string]string) bool {
	return ObjectLabelsDo(object, labels, MapDeleteF[map[string]string, string, string])
}

func ObjectLabelInsertOnlyAll(object metav1.Object, labels map[string]string) Comparison {
	return ObjectLabelsDo(object, labels, MapInsertOnlyAllF[map[string]string, string, string])
}

func ObjectLabelsDo[T any](object metav1.Object, labels map[string]string,
	do func(map[string]string, func() map[string]string, func(map[string]string)) T,
) T {
	return do(labels, object.GetLabels, object.SetLabels)
}

func ObjectOwnerSet(object, owner metav1.Object) bool {
	return ObjectLabelsSet(object, OwnerLabels(owner))
}

func ObjectOwnerSetIfNotAlready(object, owner metav1.Object) Comparison {
	return ObjectLabelInsertOnlyAll(object, OwnerLabels(owner))
}

func ObjectOwnerUnsetIfSet(object, owner metav1.Object) bool {
	return ObjectLabelsDelete(object, OwnerLabels(owner))
}

func OwnerLabels(owner metav1.Object) Labels {
	return Labels{
		LabelOwnerNamespaceName: owner.GetNamespace(),
		LabelOwnerName:          owner.GetName(),
	}
}

func OwnerNamespaceNameAndName(labels Labels) (string, string, bool) {
	ownerNamespaceName, ok1 := labels[LabelOwnerNamespaceName]
	ownerName, ok2 := labels[LabelOwnerName]

	return ownerNamespaceName, ownerName, ok1 && ok2
}

func OwnerNamespacedName(owner metav1.Object) types.NamespacedName {
	ownerNamespaceName, ownerName, _ := OwnerNamespaceNameAndName(owner.GetLabels())

	return types.NamespacedName{
		Namespace: ownerNamespaceName,
		Name:      ownerName,
	}
}
