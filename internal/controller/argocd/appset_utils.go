// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package argocdv1alpha1hack

import clrapiv1beta1 "open-cluster-management.io/api/cluster/v1beta1"

// NestedGeneratorsMatchPlacement returns true when any generator in the slice
// carries a clusterDecisionResource whose PlacementLabel matches placementName.
func NestedGeneratorsMatchPlacement(
	generators []ApplicationSetNestedGenerator, placementName string,
) bool {
	for i := range generators {
		cdr := generators[i].ClusterDecisionResource
		if cdr != nil && cdr.LabelSelector.MatchLabels[clrapiv1beta1.PlacementLabel] == placementName {
			return true
		}
	}

	return false
}

// AppSetGeneratorMatchesPlacement returns true when the given top-level ApplicationSet generator
// references the supplied Placement name, either directly via a clusterDecisionResource generator
// or nested one level deep inside a matrix or merge combinator generator.
func AppSetGeneratorMatchesPlacement(gen *ApplicationSetGenerator, placementName string) bool {
	if gen.ClusterDecisionResource != nil &&
		gen.ClusterDecisionResource.LabelSelector.MatchLabels[clrapiv1beta1.PlacementLabel] == placementName {
		return true
	}

	if gen.Matrix != nil && NestedGeneratorsMatchPlacement(gen.Matrix.Generators, placementName) {
		return true
	}

	if gen.Merge != nil && NestedGeneratorsMatchPlacement(gen.Merge.Generators, placementName) {
		return true
	}

	return false
}
