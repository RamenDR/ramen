// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"fmt"
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

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

// validateVRGState waits until the primary cluster VRG is primary/Primary and
// the secondary cluster VRG is secondary/Secondary.
func validateVRGState(ctx types.TestContext) error {
	log := ctx.Logger()
	start := time.Now()
	name := ctx.Name()
	namespace := vrgNamespace(ctx)

	primary, err := util.GetCurrentCluster(ctx, ctx.ManagementNamespace(), name)
	if err != nil {
		return err
	}

	secondary, err := getTargetCluster(ctx, ctx.Env().Hub, ctx.Config().DRPolicy, primary.Name)
	if err != nil {
		return err
	}

	log.Debugf("Waiting until vrg \"%s/%s\" is primary/Primary in cluster %q "+
		"and secondary/Secondary in cluster %q",
		namespace, name, primary.Name, secondary.Name)

	for {
		primaryVRG, primaryErr := getVRG(ctx, primary, namespace, name)
		secondaryVRG, secondaryErr := getVRG(ctx, secondary, namespace, name)

		if primaryErr == nil && secondaryErr == nil &&
			vrgHasState(primaryVRG, ramen.Primary, ramen.PrimaryState) &&
			vrgHasState(secondaryVRG, ramen.Secondary, ramen.SecondaryState) {
			elapsed := time.Since(start)
			log.Debugf("vrg \"%s/%s\" is primary/Primary in cluster %q "+
				"and secondary/Secondary in cluster %q in %.3f seconds",
				namespace, name, primary.Name, secondary.Name, elapsed.Seconds())

			return nil
		}

		if err := util.Sleep(ctx.Context(), util.RetryInterval); err != nil {
			return fmt.Errorf("vrg \"%s/%s\" not ready: %s, %s: %w",
				namespace, name,
				vrgClusterStatus(primary.Name, primaryVRG, primaryErr, "primary/Primary"),
				vrgClusterStatus(secondary.Name, secondaryVRG, secondaryErr, "secondary/Secondary"),
				err)
		}
	}
}

func vrgHasState(vrg *ramen.VolumeReplicationGroup, spec ramen.ReplicationState, state ramen.State) bool {
	return vrg.Spec.ReplicationState == spec && vrg.Status.State == state
}

func vrgClusterStatus(clusterName string, vrg *ramen.VolumeReplicationGroup, err error, expected string) string {
	if err != nil {
		return fmt.Sprintf("cluster %q: %s", clusterName, err)
	}

	return fmt.Sprintf("cluster %q is %s/%s (expected %s)",
		clusterName, vrg.Spec.ReplicationState, vrg.Status.State, expected)
}

func vrgNamespace(ctx types.TestContext) string {
	if ctx.Deployer().IsDiscovered() {
		return ctx.ManagementNamespace()
	}

	return ctx.AppNamespace()
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
