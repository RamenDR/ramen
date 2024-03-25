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

// pvcSelector depends on vrg.Namespace, vrg.Spec.KubeObjectProtection.RecipeRef and ramenConfig.MultiNamespace.FeatureEnabled
// We have 8 scenarios to consider:
// VRG NS != admin NS, RecipeRef == nil, MultiNamespace=N. Result: [vrg.Namespace]. Current Default.
// VRG NS != admin NS, RecipeRef == nil, MultiNamespace=Y. Result: Error
// VRG NS != admin NS, RecipeRef != nil, MultiNamespace=N. Result: Error
// VRG NS != admin NS, RecipeRef != nil, MultiNamespace=Y. Result: Error
// VRG NS == admin NS, RecipeRef != nil, MultiNamespace=Y. Result: vrg.Spec.ProtectedNamespaces.
// VRG NS == admin NS, RecipeRef == nil, MultiNamespace=Y. Result: Error.
// VRG NS == admin NS, RecipeRef != nil, MultiNamespace=N. Result: vrg.Spec.ProtectedNamespaces.
// VRG NS == admin NS, RecipeRef == nil, MultiNamespace=N. Result: Error.

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
