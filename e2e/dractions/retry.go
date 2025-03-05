// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"fmt"
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func waitDRPCReady(ctx types.Context, cluster util.Cluster, namespace string, drpcName string) error {
	log := ctx.Logger()
	startTime := time.Now()

	log.Debugf("Waiting until drpc \"%s/%s\" is ready in cluster %q", namespace, drpcName, cluster.Name)

	for {
		drpc, err := getDRPC(cluster, namespace, drpcName)
		if err != nil {
			return err
		}

		available := conditionMet(drpc.Status.Conditions, ramen.ConditionAvailable)
		peerReady := conditionMet(drpc.Status.Conditions, ramen.ConditionPeerReady)

		if available && peerReady && drpc.Status.LastGroupSyncTime != nil {
			log.Debugf("drpc \"%s/%s\" is ready in cluster %q", namespace, drpcName, cluster.Name)

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("timeout waiting for drpc to become ready in cluster %q"+
				" (Available: %v, PeerReady: %v, lastGroupSyncTime: %v)",
				cluster.Name, available, peerReady, drpc.Status.LastGroupSyncTime)
		}

		time.Sleep(util.RetryInterval)
	}
}

func conditionMet(conditions []metav1.Condition, conditionType string) bool {
	condition := meta.FindStatusCondition(conditions, conditionType)

	return condition != nil && condition.Status == "True"
}

func waitDRPCPhase(ctx types.Context, cluster util.Cluster, namespace, name string, phase ramen.DRState) error {
	log := ctx.Logger()
	startTime := time.Now()

	log.Debugf("Waiting until drpc \"%s/%s\" reach phase %q in cluster %q", namespace, name, phase, cluster.Name)

	for {
		drpc, err := getDRPC(cluster, namespace, name)
		if err != nil {
			return err
		}

		currentPhase := drpc.Status.Phase
		if currentPhase == phase {
			log.Debugf("drpc \"%s/%s\" phase is %q in cluster %q", namespace, name, phase, cluster.Name)

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("drpc %q status is not %q yet before timeout in cluster %q, fail", name, phase, cluster.Name)
		}

		time.Sleep(util.RetryInterval)
	}
}

// return dr cluster
func getDRCluster(clusterName string, drpolicy *ramen.DRPolicy) util.Cluster {
	if clusterName == drpolicy.Spec.DRClusters[0] {
		return util.Ctx.C1
	}

	return util.Ctx.C2
}

func getTargetCluster(cluster util.Cluster, currentCluster string) (string, error) {
	drpolicy, err := util.GetDRPolicy(cluster, config.GetDRPolicyName())
	if err != nil {
		return "", err
	}

	var targetCluster string
	if currentCluster == drpolicy.Spec.DRClusters[0] {
		targetCluster = drpolicy.Spec.DRClusters[1]
	} else {
		targetCluster = drpolicy.Spec.DRClusters[0]
	}

	return targetCluster, nil
}

func waitDRPCDeleted(ctx types.Context, cluster util.Cluster, namespace string, name string) error {
	log := ctx.Logger()
	startTime := time.Now()

	log.Debugf("Waiting until drpc \"%s/%s\" is deleted in cluster %q", namespace, name, cluster.Name)

	for {
		_, err := getDRPC(cluster, namespace, name)
		if err != nil {
			if errors.IsNotFound(err) {
				log.Debugf("drpc \"%s/%s\" is deleted in cluster %q", namespace, name, cluster.Name)

				return nil
			}

			log.Debugf("Failed to get drpc \"%s/%s\" in cluster %q: %s", namespace, name, cluster.Name, err)
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("drpc %q is not deleted yet before timeout in cluster %q, fail", name, cluster.Name)
		}

		time.Sleep(util.RetryInterval)
	}
}

// nolint:unparam
func waitDRPCProgression(
	ctx types.Context,
	cluster util.Cluster,
	namespace, name string,
	progression ramen.ProgressionStatus,
) error {
	log := ctx.Logger()
	startTime := time.Now()

	log.Debugf("Waiting until drpc \"%s/%s\" reach progression %q in cluster %q",
		namespace, name, progression, cluster.Name)

	for {
		drpc, err := getDRPC(cluster, namespace, name)
		if err != nil {
			return err
		}

		currentProgression := drpc.Status.Progression
		if currentProgression == progression {
			log.Debugf("drpc \"%s/%s\" progression is %q in cluster %q", namespace, name, progression, cluster.Name)

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("drpc %q progression is not %q yet before timeout of %v in cluster %q",
				name, progression, util.Timeout, cluster.Name)
		}

		time.Sleep(util.RetryInterval)
	}
}
