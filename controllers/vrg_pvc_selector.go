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

func pvcNamespaceNamesDefault(vrg ramen.VolumeReplicationGroup, ramenConfig ramen.RamenConfig) []string {
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
