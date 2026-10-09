// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"fmt"
	"slices"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

// clusterNameLabelKey is the label key used by the Placement predicate to select a cluster by name.
const clusterNameLabelKey = "name"

func validateVRGs(ctx types.TestContext, primary, secondary *types.Cluster) error {
	if err := validateVRG(ctx, primary, ramen.Primary, ramen.PrimaryState); err != nil {
		return err
	}

	return validateVRG(ctx, secondary, ramen.Secondary, ramen.SecondaryState)
}

func validateVRG(
	ctx types.TestContext,
	cluster *types.Cluster,
	desiredState ramen.ReplicationState,
	actualState ramen.State,
) error {
	log := ctx.Logger()
	name := ctx.Name()
	namespace := vrgNamespace(ctx)

	vrg, err := getVRG(ctx, cluster, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get vrg \"%s/%s\" in cluster %q: %w",
			namespace, name, cluster.Name, err)
	}

	if vrg.Spec.ReplicationState != desiredState || vrg.Status.State != actualState {
		return fmt.Errorf("vrg \"%s/%s\" in cluster %q is %s/%s, expected %s/%s",
			namespace, name, cluster.Name,
			vrg.Spec.ReplicationState, vrg.Status.State, desiredState, actualState)
	}

	log.Debugf("vrg \"%s/%s\" is %s/%s in cluster %q",
		namespace, name, desiredState, actualState, cluster.Name)

	return nil
}

// validatePlacementPredicate verifies that the "name In [...]" predicate of the user Placement selects only the
// cluster where the workload is running. When protection is disabled, Ramen aligns the predicate with the
// PlacementDecision, so that the Placement keeps selecting the cluster the workload was running on after the
// DRPC is gone, even if the workload was failed over or relocated since the Placement was created.
func validatePlacementPredicate(ctx types.TestContext, cluster *types.Cluster) error {
	log := ctx.Logger()
	name := ctx.Name()
	namespace := ctx.ManagementNamespace()

	placement, err := util.GetPlacement(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get placement \"%s/%s\": %w", namespace, name, err)
	}

	checked := false

	for _, predicate := range placement.Spec.Predicates {
		for _, expr := range predicate.RequiredClusterSelector.LabelSelector.MatchExpressions {
			if expr.Key != clusterNameLabelKey || expr.Operator != metav1.LabelSelectorOpIn {
				continue
			}

			if !slices.Equal(expr.Values, []string{cluster.Name}) {
				return fmt.Errorf("placement \"%s/%s\" predicate %s %s %v does not match cluster %q",
					namespace, name, expr.Key, expr.Operator, expr.Values, cluster.Name)
			}

			checked = true
		}
	}

	if !checked {
		log.Debugf("Placement \"%s/%s\" has no %q predicate, skipping validation",
			namespace, name, clusterNameLabelKey)

		return nil
	}

	log.Debugf("Placement \"%s/%s\" predicate matches cluster %q", namespace, name, cluster.Name)

	return nil
}

func vrgNamespace(ctx types.TestContext) string {
	if ctx.Deployer().IsDiscovered() {
		return ctx.ManagementNamespace()
	}

	return ctx.AppNamespace()
}
