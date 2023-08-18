// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PvcSelector struct {
	LabelSelector  metav1.LabelSelector
	NamespaceNames []string
}

func pvcNamespaceNamesDefault(vrg ramen.VolumeReplicationGroup) []string {
	return []string{vrg.Namespace}
}

func pvcSelectorDefault(vrg ramen.VolumeReplicationGroup) PvcSelector {
	return PvcSelector{vrg.Spec.PVCSelector, pvcNamespaceNamesDefault(vrg)}
}

func pvcSelectorRecipeRefNonNil(recipe recipe.Recipe, vrg ramen.VolumeReplicationGroup) PvcSelector {
	if recipe.Spec.Volumes == nil {
		return pvcSelectorDefault(vrg)
	}

	var selector PvcSelector

	if recipe.Spec.Volumes.LabelSelector != nil {
		selector.LabelSelector = *recipe.Spec.Volumes.LabelSelector
	}

	if len(recipe.Spec.Volumes.IncludedNamespaces) > 0 {
		selector.NamespaceNames = recipe.Spec.Volumes.IncludedNamespaces
	} else {
		selector.NamespaceNames = pvcNamespaceNamesDefault(vrg)
	}

	return selector
}
