// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"fmt"
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/ramendr/ramen/e2e/types"
)

func GetVRG(ctx types.Context, cluster *types.Cluster, namespace, name string) (*ramen.VolumeReplicationGroup, error) {
	vrg := &ramen.VolumeReplicationGroup{}
	key := k8stypes.NamespacedName{Namespace: namespace, Name: name}

	err := cluster.Client.Get(ctx.Context(), key, vrg)
	if err != nil {
		return nil, err
	}

	return vrg, nil
}

func vrgSecondaryReady(vrg *ramen.VolumeReplicationGroup) bool {
	return vrg.Status.State == ramen.SecondaryState &&
		vrg.Status.ObservedGeneration == vrg.Generation
}

// WaitVRGSecondaryOnCluster waits until the VRG on cluster is secondary or deleted.
// This mirrors ensureVRGIsSecondaryOnCluster in the DRPC controller.
func WaitVRGSecondaryOnCluster(ctx types.Context, cluster *types.Cluster, namespace, name string) error {
	log := ctx.Logger()
	start := time.Now()

	log.Debugf("Waiting until vrg \"%s/%s\" is secondary in cluster %q", namespace, name, cluster.Name)

	for {
		vrg, err := GetVRG(ctx, cluster, namespace, name)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				elapsed := time.Since(start)
				log.Debugf("vrg \"%s/%s\" not found in cluster %q in %.3f seconds",
					namespace, name, cluster.Name, elapsed.Seconds())

				return nil
			}

			return err
		}

		if vrgSecondaryReady(vrg) {
			elapsed := time.Since(start)
			log.Debugf("vrg \"%s/%s\" is secondary in cluster %q in %.3f seconds",
				namespace, name, cluster.Name, elapsed.Seconds())

			return nil
		}

		if err := Sleep(ctx.Context(), RetryInterval); err != nil {
			return fmt.Errorf("vrg %q is not secondary in cluster %q (spec: %q, status: %q, generation: %d, observed: %d): %w",
				name, cluster.Name, vrg.Spec.ReplicationState, vrg.Status.State,
				vrg.Generation, vrg.Status.ObservedGeneration, err)
		}
	}
}
