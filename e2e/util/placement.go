// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"
	"time"

	k8stypes "k8s.io/apimachinery/pkg/types"
	"open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/e2e/types"
)

// GetCurrentCluster returns the name of the cluster where the workload is currently placed,
// based on the PlacementDecision for the given Placement resource.
// Assumes the PlacementDecision exists with a Decision.
// Not applicable for discovered apps before enabling protection, as no Placement exists.
func GetCurrentCluster(cluster types.Cluster, namespace string, placementName string) (string, error) {
	placementDecision, err := waitPlacementDecision(cluster, namespace, placementName)
	if err != nil {
		return "", err
	}

	return placementDecision.Status.Decisions[0].ClusterName, nil
}

func GetPlacement(cluster types.Cluster, namespace, name string) (*v1beta1.Placement, error) {
	placement := &v1beta1.Placement{}
	key := k8stypes.NamespacedName{Namespace: namespace, Name: name}

	err := cluster.Client.Get(context.Background(), key, placement)
	if err != nil {
		return nil, err
	}

	return placement, nil
}

// waitPlacementDecision waits until we have a placement decision and returns the placement decision object.
func waitPlacementDecision(cluster types.Cluster, namespace string, placementName string,
) (*v1beta1.PlacementDecision, error) {
	startTime := time.Now()

	for {
		placement, err := GetPlacement(cluster, namespace, placementName)
		if err != nil {
			return nil, err
		}

		placementDecision, err := getPlacementDecisionFromPlacement(cluster, placement)
		if err != nil {
			return nil, err
		}

		if placementDecision != nil && len(placementDecision.Status.Decisions) > 0 {
			return placementDecision, nil
		}

		if time.Since(startTime) > Timeout {
			return nil, fmt.Errorf("timeout waiting for placement decisions for %q in cluster %q", placementName, cluster.Name)
		}

		time.Sleep(RetryInterval)
	}
}

func getPlacementDecisionFromPlacement(cluster types.Cluster, placement *v1beta1.Placement,
) (*v1beta1.PlacementDecision, error) {
	matchLabels := map[string]string{
		v1beta1.PlacementLabel: placement.GetName(),
	}

	listOptions := []client.ListOption{
		client.InNamespace(placement.GetNamespace()),
		client.MatchingLabels(matchLabels),
	}

	plDecisions := &v1beta1.PlacementDecisionList{}
	if err := cluster.Client.List(context.Background(), plDecisions, listOptions...); err != nil {
		return nil, fmt.Errorf("failed to list PlacementDecisions (placement: %s) in cluster %q",
			placement.GetNamespace()+"/"+placement.GetName(), cluster.Name)
	}

	if len(plDecisions.Items) == 0 {
		return nil, nil
	}

	if len(plDecisions.Items) > 1 {
		return nil, fmt.Errorf("multiple PlacementDecisions found for Placement (count: %d, placement: %s) in cluster %q",
			len(plDecisions.Items), placement.GetNamespace()+"/"+placement.GetName(), cluster.Name)
	}

	plDecision := plDecisions.Items[0]
	// r.Log.Info("Found ClusterDecision", "ClsDedicision", plDecision.Status.Decisions)

	if len(plDecision.Status.Decisions) > 1 {
		return nil, fmt.Errorf("multiple placements found in PlacementDecision"+
			" (count: %d, Placement: %s, PlacementDecision: %s) in cluster %q",
			len(plDecision.Status.Decisions),
			placement.GetNamespace()+"/"+placement.GetName(),
			plDecision.GetName()+"/"+plDecision.GetNamespace(), cluster.Name)
	}

	return &plDecision, nil
}
