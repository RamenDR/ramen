// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package dractions

import (
	"fmt"
	"time"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

type drpcReadyState struct {
	available         metav1.ConditionStatus
	peerReady         metav1.ConditionStatus
	protected         metav1.ConditionStatus
	progression       ramen.ProgressionStatus
	lastGroupSyncTime *metav1.Time
}

func waitDRPCReady(ctx types.TestContext, namespace string, drpcName string) error {
	log := ctx.Logger()
	hub := ctx.Env().Hub
	start := time.Now()

	log.Debugf("Waiting until drpc \"%s/%s\" is ready in cluster %q", namespace, drpcName, hub.Name)

	var prev drpcReadyState

	logInitialState := true

	for {
		drpc, err := getDRPC(ctx, namespace, drpcName)
		if err != nil {
			return err
		}

		curr := newDRPCReadyState(drpc)
		if logInitialState {
			logDRPCReadyState(log, namespace, drpcName, curr)

			logInitialState = false
		} else {
			logCondtionStatusChange(log, namespace, drpcName, ramen.ConditionAvailable, prev.available, curr.available)
			logCondtionStatusChange(log, namespace, drpcName, ramen.ConditionPeerReady, prev.peerReady, curr.peerReady)
			logCondtionStatusChange(log, namespace, drpcName, ramen.ConditionProtected, prev.protected, curr.protected)
			logProgressionChange(log, namespace, drpcName, prev.progression, curr.progression)
			logLastGroupSyncTimeChange(log, namespace, drpcName, prev.lastGroupSyncTime, curr.lastGroupSyncTime)
		}

		prev = curr

		// Not sure if checking for progression completed is needed.
		// Ideally, conditions should be enough.
		// see https://github.com/RamenDR/ramen/issues/1988
		if curr.available == metav1.ConditionTrue &&
			curr.peerReady == metav1.ConditionTrue &&
			curr.protected == metav1.ConditionTrue &&
			curr.progression == ramen.ProgressionCompleted &&
			curr.lastGroupSyncTime != nil {
			elapsed := time.Since(start)
			log.Debugf("drpc \"%s/%s\" is ready in cluster %q in %.3f seconds",
				namespace, drpcName, hub.Name, elapsed.Seconds())

			return nil
		}

		if err := util.Sleep(ctx.Context(), util.RetryInterval); err != nil {
			return fmt.Errorf("drpc not ready in cluster %q"+
				" (Available: %q, PeerReady: %q, Protected: %q, Progression: %q, lastGroupSyncTime: %v): %w",
				hub.Name, curr.available, curr.peerReady, curr.protected,
				curr.progression, curr.lastGroupSyncTime, err)
		}
	}
}

func newDRPCReadyState(drpc *ramen.DRPlacementControl) drpcReadyState {
	state := drpcReadyState{
		available:   conditionStatus(drpc.Status.Conditions, ramen.ConditionAvailable),
		peerReady:   conditionStatus(drpc.Status.Conditions, ramen.ConditionPeerReady),
		protected:   conditionStatus(drpc.Status.Conditions, ramen.ConditionProtected),
		progression: drpc.Status.Progression,
	}

	if drpc.Status.LastGroupSyncTime != nil {
		lastGroupSyncTime := *drpc.Status.LastGroupSyncTime
		state.lastGroupSyncTime = &lastGroupSyncTime
	}

	return state
}

func logDRPCReadyState(log *zap.SugaredLogger, namespace, name string, state drpcReadyState) {
	log.Debugf("drpc \"%s/%s\" state: Available=%s PeerReady=%s Protected=%s Progression=%s lastGroupSyncTime=%v",
		namespace, name, state.available, state.peerReady, state.protected, state.progression, state.lastGroupSyncTime)
}

func logCondtionStatusChange(
	log *zap.SugaredLogger,
	namespace, name, condition string,
	previous, current metav1.ConditionStatus,
) {
	if previous == current {
		return
	}

	msg := fmt.Sprintf("drpc \"%s/%s\" %q changed from %q to %q", namespace, name, condition, previous, current)

	if previous == metav1.ConditionTrue {
		log.Warnf(msg)
	} else {
		log.Debugf(msg)
	}
}

func logProgressionChange(
	log *zap.SugaredLogger,
	namespace, name string,
	previous, current ramen.ProgressionStatus,
) {
	if previous == current {
		return
	}

	msg := fmt.Sprintf("drpc \"%s/%s\" Progression changed from %q to %q", namespace, name, previous, current)

	if previous == ramen.ProgressionCompleted {
		log.Warnf(msg)
	} else {
		log.Debugf(msg)
	}
}

func logLastGroupSyncTimeChange(
	log *zap.SugaredLogger,
	namespace, name string,
	previous, current *metav1.Time,
) {
	if previous.Equal(current) {
		return
	}

	msg := fmt.Sprintf("drpc \"%s/%s\" lastGroupSyncTime changed from %v to %v", namespace, name, previous, current)

	if current == nil {
		log.Warnf(msg)
	} else {
		log.Debugf(msg)
	}
}

func conditionStatus(conditions []metav1.Condition, conditionType string) metav1.ConditionStatus {
	condition := meta.FindStatusCondition(conditions, conditionType)
	if condition == nil {
		return ""
	}

	return condition.Status
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
