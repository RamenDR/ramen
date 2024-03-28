// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PvcSelector struct {
	LabelSelector  metav1.LabelSelector
	NamespaceNames []string
}

// pvcNamespaceNamesDefault returns the default pvc namespaces for the VRG.
// If the VRG namespace is the Ramen operands namespace, then the protected namespaces are used.
// In the else cases, vrg in application namespace or the ramen operator namespace, the VRG namespace is used.
func pvcNamespaceNamesDefault(vrg ramen.VolumeReplicationGroup, ramenConfig ramen.RamenConfig) []string {
	if vrg.Namespace == RamenOperandsNamespace(ramenConfig) {
		return *vrg.Spec.ProtectedNamespaces
	}

	return []string{vrg.Namespace}
}

// getPVCSelector returns the PVC selector for the VRG. Recipe configuration overrides the VRG configuration.
func getPVCSelector(vrg ramen.VolumeReplicationGroup, ramenConfig ramen.RamenConfig,
	recipeVolNamespaces []string, recipeVolLabelSelector *metav1.LabelSelector,
) PvcSelector {
	var selector PvcSelector

	if recipeVolLabelSelector != nil {
		selector.LabelSelector = *recipeVolLabelSelector
	} else {
		selector.LabelSelector = vrg.Spec.PVCSelector
	}

	if len(recipeVolNamespaces) > 0 {
		selector.NamespaceNames = recipeVolNamespaces
	} else {
		selector.NamespaceNames = pvcNamespaceNamesDefault(vrg, ramenConfig)
	}

	return selector
}
