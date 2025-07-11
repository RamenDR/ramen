// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"fmt"
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

const drpcDoNotDeletePVC = "drplacementcontrol.ramendr.openshift.io/do-not-delete-pvc"

func waitDRPCReady(ctx types.TestContext, namespace string, drpcName string) error {
	log := ctx.Logger()
	hub := ctx.Env().Hub
	start := time.Now()

	log.Debugf("Waiting until drpc \"%s/%s\" is ready in cluster %q", namespace, drpcName, hub.Name)

	for {
		drpc, err := getDRPC(ctx, namespace, drpcName)
		if err != nil {
			return err
		}

		available := conditionMet(drpc.Status.Conditions, ramen.ConditionAvailable)
		peerReady := conditionMet(drpc.Status.Conditions, ramen.ConditionPeerReady)

		// Not sure if checking for progression completed is needed.
		// Ideally, conditions should be enough.
		// see https://github.com/RamenDR/ramen/issues/1988
		progressionCompleted := drpc.Status.Progression == ramen.ProgressionCompleted

		if available &&
			peerReady &&
			progressionCompleted &&
			drpc.Status.LastGroupSyncTime != nil {
			elapsed := time.Since(start)
			log.Debugf("drpc \"%s/%s\" is ready in cluster %q in %.3f seconds",
				namespace, drpcName, hub.Name, elapsed.Seconds())

			return nil
		}

		if err := util.Sleep(ctx.Context(), util.RetryInterval); err != nil {
			return fmt.Errorf("drpc not ready in cluster %q"+
				" (Available: %v, PeerReady: %v, ProgressionCompleted: %v, lastGroupSyncTime: %v): %w",
				hub.Name, available, peerReady, progressionCompleted, drpc.Status.LastGroupSyncTime, err)
		}
	}
}

func conditionMet(conditions []metav1.Condition, conditionType string) bool {
	condition := meta.FindStatusCondition(conditions, conditionType)

	return condition != nil && condition.Status == "True"
}

func waitDRPCPhase(ctx types.TestContext, namespace, name string, phase ramen.DRState) error {
	log := ctx.Logger()
	hub := ctx.Env().Hub
	start := time.Now()

	log.Debugf("Waiting until drpc \"%s/%s\" reach phase %q in cluster %q", namespace, name, phase, hub.Name)

	for {
		drpc, err := getDRPC(ctx, namespace, name)
		if err != nil {
			return err
		}

		currentPhase := drpc.Status.Phase
		if currentPhase == phase {
			elapsed := time.Since(start)
			log.Debugf("drpc \"%s/%s\" phase is %q in cluster %q in %.3f seconds",
				namespace, name, phase, hub.Name, elapsed.Seconds())

			return nil
		}

		if err := util.Sleep(ctx.Context(), util.RetryInterval); err != nil {
			return fmt.Errorf("drpc %q phase is not %q in cluster %q: %w", name, phase, hub.Name, err)
		}
	}
}

func getTargetCluster(
	ctx types.TestContext,
	cluster *types.Cluster,
	drPolicyName, currentCluster string,
) (*types.Cluster, error) {
	drpolicy, err := util.GetDRPolicy(ctx, cluster, drPolicyName)
	if err != nil {
		return nil, err
	}

	var targetClusterName string
	if currentCluster == drpolicy.Spec.DRClusters[0] {
		targetClusterName = drpolicy.Spec.DRClusters[1]
	} else {
		targetClusterName = drpolicy.Spec.DRClusters[0]
	}

	return ctx.Env().GetCluster(targetClusterName)
}

// nolint:unparam
func waitDRPCProgression(
	ctx types.TestContext,
	namespace, name string,
	progression ramen.ProgressionStatus,
) error {
	log := ctx.Logger()
	hub := ctx.Env().Hub
	start := time.Now()

	log.Debugf("Waiting until drpc \"%s/%s\" reach progression %q in cluster %q",
		namespace, name, progression, hub.Name)

	for {
		drpc, err := getDRPC(ctx, namespace, name)
		if err != nil {
			return err
		}

		currentProgression := drpc.Status.Progression
		if currentProgression == progression {
			elapsed := time.Since(start)
			log.Debugf("drpc \"%s/%s\" progression is %q in cluster %q in %.3f seconds",
				namespace, name, progression, hub.Name, elapsed.Seconds())

			return nil
		}

		if err := util.Sleep(ctx.Context(), util.RetryInterval); err != nil {
			return fmt.Errorf("drpc %q progression is not %q in cluster %q: %w",
				name, progression, hub.Name, err)
		}
	}
}

func addDRPCAnnotation(ctx types.TestContext, namespace, drpcName string) error {
	log := ctx.Logger()

	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		drpc, err := getDRPC(ctx, namespace, drpcName)
		if err != nil {
			return err
		}

		if drpc.Annotations == nil {
			drpc.Annotations = make(map[string]string)
		}

		drpc.Annotations[drpcDoNotDeletePVC] = "true"

		if err := updateDRPC(ctx, drpc); err != nil {
			return err
		}

		log.Debugf("Annotated drpc \"%s/%s\" with \"%s: %s\" in cluster %q",
			namespace, drpcName, drpcDoNotDeletePVC,
			drpc.Annotations[drpcDoNotDeletePVC], ctx.Env().Hub.Name)

		return nil
	})
}

func waitForDRPCAnnotationPropagation(ctx types.TestContext, namespace, drpcName string) error {
	log := ctx.Logger()
	start := time.Now()

	log.Debugf("Waiting until drpc \"%s/%s\" annotation %q is propagated to primary vrg",
		namespace, drpcName, drpcDoNotDeletePVC)

	for {
		drpc, err := getDRPC(ctx, namespace, drpcName)
		if err != nil {
			return err
		}

		preferredClusterName := drpc.Spec.PreferredCluster

		preferredCluster, err := ctx.Env().GetCluster(preferredClusterName)
		if err != nil {
			return err
		}

		vrgName := drpcName

		vrg, err := getVRG(ctx, preferredCluster, ctx.AppNamespace(), vrgName)
		if err != nil {
			return err
		}

		if vrg.Annotations != nil {
			if value, exists := vrg.Annotations[drpcDoNotDeletePVC]; exists && value == "true" {
				elapsed := time.Since(start)
				log.Debugf("DRPC \"%s/%s\" annotation \"%s: %s\" propagated to primary vrg \"%s/%s\" in cluster %q in %.3f seconds",
					namespace, drpcName, drpcDoNotDeletePVC, drpc.Annotations[drpcDoNotDeletePVC], ctx.AppNamespace(),
					vrgName, preferredCluster.Name, elapsed.Seconds())

				return nil
			}
		}

		if err := util.Sleep(ctx.Context(), util.RetryInterval); err != nil {
			return fmt.Errorf("drpc \"%s/%s\" annotation \"%s: %s\" propagation failed: %w",
				namespace, drpcName, drpcDoNotDeletePVC, drpc.Annotations[drpcDoNotDeletePVC], err)
		}
	}
}
