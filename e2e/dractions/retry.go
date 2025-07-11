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

// drpcDoNotDeletePVCAnnotation prevents PVC deletion when VRG is deleted by disowning PVCs and reinstating OCM annotations.
// https://github.com/RamenDR/ramen/blob/cdd609356182aa309ce46ab9e4e7623ecb478f1c/internal/controller/drplacementcontrol_controller.go#L63
const drpcDoNotDeletePVCAnnotation = "drplacementcontrol.ramendr.openshift.io/do-not-delete-pvc"

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

func addDRPCAnnotation(ctx types.TestContext, drpcName string) error {
	log := ctx.Logger()

	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		drpc, err := getDRPC(ctx, ctx.ManagementNamespace(), drpcName)
		if err != nil {
			return err
		}

		if drpc.Annotations == nil {
			drpc.Annotations = map[string]string{}
		}

		drpc.Annotations[drpcDoNotDeletePVCAnnotation] = "true"

		if err := updateDRPC(ctx, drpc); err != nil {
			return err
		}

		log.Debugf("Annotated drpc \"%s/%s\" with \"%s: %s\" in cluster %q",
			ctx.ManagementNamespace(), drpcName, drpcDoNotDeletePVCAnnotation,
			drpc.Annotations[drpcDoNotDeletePVCAnnotation], ctx.Env().Hub.Name)

		return nil
	})
}

func waitForDRPCAnnotationPropagation(ctx types.TestContext, drpcName string) error {
	log := ctx.Logger()
	vrgName := drpcName
	vrgNamespace := getVRGNamespace(ctx)
	start := time.Now()

	drpc, err := getDRPC(ctx, ctx.ManagementNamespace(), drpcName)
	if err != nil {
		return err
	}

	preferredCluster, err := ctx.Env().GetCluster(drpc.Spec.PreferredCluster)
	if err != nil {
		return err
	}

	log.Debugf("Waiting for vrg \"%s/%s\" annotation \"%s: true\" in cluster %q",
		vrgNamespace, vrgName, drpcDoNotDeletePVCAnnotation, preferredCluster.Name)

	for {
		vrg, err := getVRG(ctx, preferredCluster, vrgNamespace, vrgName)
		if err != nil {
			return err
		}

		if vrg.Annotations[drpcDoNotDeletePVCAnnotation] == "true" {
			elapsed := time.Since(start)
			log.Debugf("vrg \"%s/%s\" annotation \"%s: true\" propagated in cluster %q in %.3f seconds",
				vrgNamespace, vrgName, drpcDoNotDeletePVCAnnotation, preferredCluster.Name, elapsed.Seconds())

			return nil
		}

		if err := util.Sleep(ctx.Context(), util.RetryInterval); err != nil {
			return fmt.Errorf("vrg \"%s/%s\" annotation \"%s: true\" not propagated in cluster %q: %w",
				vrgNamespace, vrgName, drpcDoNotDeletePVCAnnotation, preferredCluster.Name, err)
		}
	}
}
