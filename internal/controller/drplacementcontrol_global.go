// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
)

func (d *DRPCInstance) globalVGRLabel() string {
	return d.instance.GetLabels()[GlobalVGRLabel]
}

func (d *DRPCInstance) hasGlobalVGRLabel() bool {
	return d.globalVGRLabel() != ""
}

// ensureGlobalVGRLabel propagates the global VGR label from the primary VRG to the DRPC,
// enabling DRPC level consensus checks.
func (d *DRPCInstance) ensureGlobalVGRLabel() bool {
	if d.hasGlobalVGRLabel() {
		return true
	}

	for _, vrg := range d.vrgs {
		if vrg.Spec.ReplicationState != rmn.Primary {
			continue
		}

		if vgrLabel := vrg.GetLabels()[GlobalVGRLabel]; vgrLabel != "" {
			rmnutil.AddLabel(d.instance, GlobalVGRLabel, vgrLabel)

			if err := d.reconciler.Update(d.ctx, d.instance); err != nil {
				d.log.Error(err, "Failed to add global VGR label", "label", vgrLabel)

				return false
			}

			d.log.Info("Added global VGR label", "label", vgrLabel)

			// Requeue so the reconciler picks up the updated label
			return false
		}

		break
	}

	return true
}

// isGlobalActionInConsensus checks that all DRPCs sharing the same global VGR label
// have the same action and target cluster. This prevents any single DRPC from proceeding
// with an action until all DRPCs with the same label agree.
//
//nolint:cyclop
func (d *DRPCInstance) isGlobalActionInConsensus() bool {
	vgrLabel := d.globalVGRLabel()
	action := d.instance.Spec.Action

	if action != rmn.ActionFailover && action != rmn.ActionRelocate {
		d.log.Info("Action not supported for global action consensus", "action", action)

		return false
	}

	targetCluster := d.instance.Spec.FailoverCluster
	if action == rmn.ActionRelocate {
		targetCluster = d.instance.Spec.PreferredCluster
	}

	log := d.log.WithName("GlobalActionConsensus").WithValues(
		"label", vgrLabel, "action", action, "targetCluster", targetCluster)

	var drpcs rmn.DRPlacementControlList

	if err := d.reconciler.List(d.ctx, &drpcs,
		client.MatchingLabels{GlobalVGRLabel: vgrLabel},
	); err != nil {
		log.Error(err, "Failed to list DRPCs")

		return false
	}

	var pending []string

	for idx := range drpcs.Items {
		drpc := &drpcs.Items[idx]
		if drpc.Name == d.instance.Name && drpc.Namespace == d.instance.Namespace {
			continue
		}

		if drpc.Spec.Action != action ||
			(action == rmn.ActionFailover && drpc.Spec.FailoverCluster != targetCluster) ||
			(action == rmn.ActionRelocate && drpc.Spec.PreferredCluster != targetCluster) {
			pending = append(pending, drpc.Namespace+"/"+drpc.Name)
		}
	}

	if len(pending) > 0 {
		msg := fmt.Sprintf("Pending: %s; expected action %s to %s",
			strings.Join(pending, ", "), action, targetCluster)
		log.Info(msg)
		d.setGlobalActionCondition(false, msg)
		d.setProgression(rmn.ProgressionWaitOnGlobalAction)

		return false
	}

	msg := fmt.Sprintf("Consensus reached for action %s", action)
	log.Info(msg, "count", len(drpcs.Items))
	d.setGlobalActionCondition(true, msg)

	return true
}

func (d *DRPCInstance) setGlobalActionCondition(met bool, message string) {
	status := metav1.ConditionFalse
	reason := ConditionReasonConsensusNotReached

	if met {
		status = metav1.ConditionTrue
		reason = ConditionReasonConsensusReached
	}

	addOrUpdateCondition(&d.instance.Status.Conditions, rmn.ConditionGlobalAction,
		d.instance.Generation, status, reason, message)
}
