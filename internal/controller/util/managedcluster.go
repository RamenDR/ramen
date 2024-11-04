// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"
	"strings"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ocmv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Prefixes for various ClusterClaims
	CCSCPrefix  = "storage.class"
	CCVSCPrefix = "snapshot.class"
	CCVRCPrefix = "replication.class"
)

type ManagedClusterInstance struct {
	object *ocmv1.ManagedCluster
}

// NewManagedClusterInstance creates an ManagedClusterInstance instance, reading the ManagedCluster resource for cluster
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

// ClusterID returns the clusterID claimed by the ManagedCluster, or error if it is empty or not found
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

// classClaims returns a list of class claims with the passed in prefix from the ManagedCluster
func (mci *ManagedClusterInstance) classClaims(prefix string) []string {
	classNames := []string{}

	for idx := range mci.object.Status.ClusterClaims {
		if !strings.HasPrefix(mci.object.Status.ClusterClaims[idx].Name, prefix+".") {
			continue
		}

		className := strings.TrimPrefix(mci.object.Status.ClusterClaims[idx].Name, prefix+".")
		if className == "" {
			continue
		}

		classNames = append(classNames, className)
	}

	return classNames
}

func (mci *ManagedClusterInstance) StorageClassClaims() []string {
	return mci.classClaims(CCSCPrefix)
}

func (mci *ManagedClusterInstance) VolumeSnapshotClassClaims() []string {
	return mci.classClaims(CCVSCPrefix)
}

func (mci *ManagedClusterInstance) VolumeReplicationClassClaims() []string {
	return mci.classClaims(CCVRCPrefix)
}
