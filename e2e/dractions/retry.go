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

func waitDRPCReady(ctx types.Context, client client.Client, namespace string, drpcName string) error {
	log := ctx.Logger()
	startTime := time.Now()

	log.Info("Waiting until drpc is ready")

	for {
		drpc, err := getDRPC(client, namespace, drpcName)
		if err != nil {
			return err
		}

		available := conditionMet(drpc.Status.Conditions, ramen.ConditionAvailable)
		peerReady := conditionMet(drpc.Status.Conditions, ramen.ConditionPeerReady)

		if available && peerReady && drpc.Status.LastGroupSyncTime != nil {
			log.Info("drpc is ready")

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

func waitDRPCPhase(ctx types.Context, client client.Client, namespace, name string, phase ramen.DRState) error {
	log := ctx.Logger()
	startTime := time.Now()

	for {
		drpc, err := getDRPC(client, namespace, name)
		if err != nil {
			return err
		}

		currentPhase := drpc.Status.Phase
		if currentPhase == phase {
			log.Infof("drpc phase is %q", phase)

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

func waitDRPCDeleted(ctx types.Context, client client.Client, namespace string, name string) error {
	log := ctx.Logger()
	startTime := time.Now()

	for {
		_, err := getDRPC(client, namespace, name)
		if err != nil {
			if errors.IsNotFound(err) {
				log.Info("drpc is deleted")

				return nil
			}

			log.Infof("Failed to get drpc: %s", err)
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
	client client.Client,
	namespace, name string,
	progression ramen.ProgressionStatus,
) error {
	log := ctx.Logger()
	startTime := time.Now()

	for {
		drpc, err := getDRPC(client, namespace, name)
		if err != nil {
			return err
		}

		currentProgression := drpc.Status.Progression
		if currentProgression == progression {
			log.Infof("drpc progression is %q", progression)

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("drpc %q progression is not %q yet before timeout of %v",
				name, progression, util.Timeout)
		}

		time.Sleep(util.RetryInterval)
	}
}
