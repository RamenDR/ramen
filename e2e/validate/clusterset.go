// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package validate

import (
	"context"
	"fmt"

	ocmv1 "open-cluster-management.io/api/cluster/v1"
	ocmv1b2 "open-cluster-management.io/api/cluster/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/e2e/types"
)

func getClusterSet(hub types.Cluster, clusterSetName string) (*ocmv1b2.ManagedClusterSet, error) {
	clusterSet := &ocmv1b2.ManagedClusterSet{}
	key := client.ObjectKey{Name: clusterSetName}

	if err := hub.Client.Get(context.TODO(), key, clusterSet); err != nil {
		return nil, fmt.Errorf("failed to get ClusterSet %q: %w", clusterSetName, err)
	}

	return clusterSet, nil
}

func getManagedClustersFromClusterSet(hub types.Cluster, clusterSetName string) ([]string, error) {
	clusterList := &ocmv1.ManagedClusterList{}
	labelSelector := client.MatchingLabels{"cluster.open-cluster-management.io/clusterset": clusterSetName}

	if err := hub.Client.List(context.TODO(), clusterList, labelSelector); err != nil {
		return nil, fmt.Errorf("failed to list ManagedClusters for ClusterSet %q: %w", clusterSetName, err)
	}

	if len(clusterList.Items) == 0 {
		return nil, fmt.Errorf("no clusters found for ClusterSet %q", clusterSetName)
	}

	clusterNames := make([]string, 0, len(clusterList.Items))
	for _, cluster := range clusterList.Items {
		clusterNames = append(clusterNames, cluster.Name)
	}

	return clusterNames, nil
}
