// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ocmv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ManagedClusterInstance struct {
	object *ocmv1.ManagedCluster
}

func NewManagedClusterInstance(
	ctx context.Context,
	client client.Client,
	cluster string,
) (*ManagedClusterInstance, error) {
	mc := &ocmv1.ManagedCluster{}

	err := client.Get(ctx, types.NamespacedName{Name: cluster}, mc)
	if err != nil {
		return nil, fmt.Errorf("failed to get ManagedCluster resource for cluster (%s): %w", cluster, err)
	}

	joined := false

	for idx := range mc.Status.Conditions {
		if mc.Status.Conditions[idx].Type != ocmv1.ManagedClusterConditionJoined {
			continue
		}

		if mc.Status.Conditions[idx].Status != v1.ConditionTrue {
			return nil, fmt.Errorf("cluster (%s) has not joined the hub", cluster)
		}

		joined = true

		break
	}

	if !joined {
		return nil, fmt.Errorf("cluster (%s) has not joined the hub", cluster)
	}

	return &ManagedClusterInstance{
		object: mc,
	}, nil
}

func (mci *ManagedClusterInstance) ClusterID() (string, error) {
	id := ""

	for idx := range mci.object.Status.ClusterClaims {
		if mci.object.Status.ClusterClaims[idx].Name != "id.k8s.io" {
			continue
		}

		id = mci.object.Status.ClusterClaims[idx].Value

		break
	}

	if id == "" {
		return "", fmt.Errorf("cluster (%s) is missing cluster ID claim", mci.object.GetName())
	}

	return id, nil
}
