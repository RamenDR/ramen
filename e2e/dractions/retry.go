// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"fmt"
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func waitDRPCReady(ctx types.Context, cluster util.Cluster, namespace string, drpcName string) error {
	log := ctx.Logger()
	startTime := time.Now()

	log.Debugf("Waiting until drpc \"%s/%s\" is ready", namespace, drpcName)

	for {
		drpc, err := getDRPC(cluster, namespace, drpcName)
		if err != nil {
			return err
		}

		available := conditionMet(drpc.Status.Conditions, ramen.ConditionAvailable)
		peerReady := conditionMet(drpc.Status.Conditions, ramen.ConditionPeerReady)

		if available && peerReady && drpc.Status.LastGroupSyncTime != nil {
			log.Debugf("drpc \"%s/%s\" is ready", namespace, drpcName)

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("timeout waiting for drpc to become ready (Available: %v, PeerReady: %v, lastGroupSyncTime: %v)",
				available, peerReady, drpc.Status.LastGroupSyncTime)
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

	log.Debugf("Waiting until drpc \"%s/%s\" reach phase %q", namespace, name, phase)

	for {
		drpc, err := getDRPC(cluster, namespace, name)
		if err != nil {
			return err
		}

		currentPhase := drpc.Status.Phase
		if currentPhase == phase {
			log.Debugf("drpc \"%s/%s\" phase is %q", namespace, name, phase)

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("drpc %q status is not %q yet before timeout, fail", name, phase)
		}

		time.Sleep(util.RetryInterval)
	}
}

// return dr cluster client
func getDRClusterClient(clusterName string, drpolicy *ramen.DRPolicy) client.Client {
	if clusterName == drpolicy.Spec.DRClusters[0] {
		return util.Ctx.C1.Client
	}

	return util.Ctx.C2.Client
}

func getTargetCluster(cluster util.Cluster, currentCluster string) (string, error) {
	drpolicy, err := util.GetDRPolicy(cluster, util.DefaultDRPolicyName)
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

	log.Debugf("Waiting until drpc \"%s/%s\" is deleted", namespace, name)

	for {
		_, err := getDRPC(cluster, namespace, name)
		if err != nil {
			if errors.IsNotFound(err) {
				log.Debugf("drpc \"%s/%s\" is deleted", namespace, name)

				return nil
			}

			log.Debugf("Failed to get drpc \"%s/%s\": %s", namespace, name, err)
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("drpc %q is not deleted yet before timeout, fail", name)
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

	log.Debugf("Waiting until drpc \"%s/%s\" reach progression %q", namespace, name, progression)

	for {
		drpc, err := getDRPC(cluster, namespace, name)
		if err != nil {
			return err
		}

		currentProgression := drpc.Status.Progression
		if currentProgression == progression {
			log.Debugf("drpc \"%s/%s\" progression is %q", namespace, name, progression)

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("drpc %q progression is not %q yet before timeout of %v",
				name, progression, util.Timeout)
		}

		time.Sleep(util.RetryInterval)
	}
}
