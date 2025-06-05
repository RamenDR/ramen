// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"fmt"

	k8stypes "k8s.io/apimachinery/pkg/types"
	"open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/e2e/types"
)

// TODO: Carried over from internal/controllers, this is not part of API, may need a better way to reference it!
const PlacementDecisionReasonFailoverRetained = "RetainedForFailover"

// GetCurrentCluster retrieves the cluster object where the workload is currently placed
// by looking up the cluster name from the PlacementDecision and returning the corresponding
// cluster from the environment. Requires an existing PlacementDecision with a valid decision.
// Not applicable for discovered apps before enabling protection, as no Placement exists.
func GetCurrentCluster(ctx types.Context, namespace string, placementName string) (types.Cluster, error) {
	clusterDecision, err := getClusterDecisionFromPlacement(ctx, namespace, placementName)
	if err != nil {
		return types.Cluster{}, err
	}

	return ctx.Env().GetCluster(clusterDecision.ClusterName)
}

func GetPlacement(ctx types.Context, namespace, name string) (*v1beta1.Placement, error) {
	hub := ctx.Env().Hub

	placement := &v1beta1.Placement{}
	key := k8stypes.NamespacedName{Namespace: namespace, Name: name}

	err := hub.Client.Get(ctx.Context(), key, placement)
	if err != nil {
		return nil, err
	}

	return placement, nil
}

// getClusterDecisionFromPlacement waits until we have a placement decision and returns the cluster decision
//
//nolint:gocognit
func getClusterDecisionFromPlacement(ctx types.Context, namespace string, placementName string,
) (*v1beta1.ClusterDecision, error) {
	log := ctx.Logger()
	cluster := ctx.Env().Hub

	log.Debugf("Waiting for placement decisions for \"%s/%s\" in cluster %q", namespace, placementName, cluster.Name)

	for {
		placement, err := GetPlacement(ctx, namespace, placementName)
		if err != nil {
			return nil, err
		}

		placementDecision, err := getPlacementDecisionFromPlacement(ctx, placement)
		if err != nil {
			return nil, err
		}

		if placementDecision != nil {
			for idx := range placementDecision.Status.Decisions {
				if placementDecision.Status.Decisions[idx].Reason == PlacementDecisionReasonFailoverRetained {
					continue
				}

				return &placementDecision.Status.Decisions[idx], nil
			}
		}

		if err := Sleep(ctx.Context(), RetryInterval); err != nil {
			return nil, fmt.Errorf("no placement decisions for %q in cluster %q: %w",
				placementName, cluster.Name, err)
		}
	}
}

func getPlacementDecisionFromPlacement(ctx types.Context, placement *v1beta1.Placement,
) (*v1beta1.PlacementDecision, error) {
	hub := ctx.Env().Hub

	matchLabels := map[string]string{
		v1beta1.PlacementLabel: placement.GetName(),
	}

	listOptions := []client.ListOption{
		client.InNamespace(placement.GetNamespace()),
		client.MatchingLabels(matchLabels),
	}

	plDecisions := &v1beta1.PlacementDecisionList{}
	if err := hub.Client.List(ctx.Context(), plDecisions, listOptions...); err != nil {
		return nil, fmt.Errorf("failed to list PlacementDecisions (placement: %s) in cluster %q",
			placement.GetNamespace()+"/"+placement.GetName(), hub.Name)
	}

	if len(plDecisions.Items) == 0 {
		return nil, nil
	}

	if len(plDecisions.Items) > 1 {
		return nil, fmt.Errorf("multiple PlacementDecisions found for Placement (count: %d, placement: %s) in cluster %q",
			len(plDecisions.Items), placement.GetNamespace()+"/"+placement.GetName(), hub.Name)
	}

	plDecision := plDecisions.Items[0]
	// r.Log.Info("Found ClusterDecision", "ClsDedicision", plDecision.Status.Decisions)

	decisionCount := 0

	for idx := range plDecision.Status.Decisions {
		if plDecision.Status.Decisions[idx].Reason == PlacementDecisionReasonFailoverRetained {
			continue
		}

		decisionCount++
	}

	if decisionCount > 1 {
		return nil, fmt.Errorf("multiple placements found in PlacementDecision"+
			" (count: %d, Placement: %s, PlacementDecision: %s) in cluster %q",
			decisionCount,
			placement.GetNamespace()+"/"+placement.GetName(),
			plDecision.GetName()+"/"+plDecision.GetNamespace(), hub.Name)
	}

	return &plDecision, nil
}
