// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"encoding/json"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
)

// DRActionCountAnnotation persists the cumulative failover and relocate
// action counts on a DRPC, so that the counts survive operator restarts and
// hub recovery. lastPhase records the last accounted status.phase, ensuring
// each initiated action is counted exactly once across retried reconciles.
const DRActionCountAnnotation = "drplacementcontrol.ramendr.openshift.io/dr-action-count"

type drActionCount struct {
	Failover  uint64      `json:"failover"`
	Relocate  uint64      `json:"relocate"`
	LastPhase rmn.DRState `json:"lastPhase"`
}

func parseDRActionCount(drpc *rmn.DRPlacementControl) drActionCount {
	counts := drActionCount{}

	value, ok := drpc.GetAnnotations()[DRActionCountAnnotation]
	if !ok {
		return counts
	}

	if err := json.Unmarshal([]byte(value), &counts); err != nil {
		// Self-heal from a malformed annotation by restarting the counts
		return drActionCount{}
	}

	return counts
}

// syncDRActionCountAnnotation updates the DRActionCountAnnotation on the DRPC
// in memory from its status.phase, and returns whether the annotation changed
// and needs to be persisted.
//
// A new DR action always starts by transitioning into Initiating (see
// setStatusInitiating), so an action is counted exactly when Initiating
// transitions into FailingOver or Relocating. Direct re-entries into an
// action phase (e.g. Relocated -> Relocating while post-action cleanup is in
// progress) are phase flapping, not new actions, and are ignored. DRPCs that
// never initiate an action never gain the annotation.
func syncDRActionCountAnnotation(drpc *rmn.DRPlacementControl) bool {
	phase := drpc.Status.Phase
	counts := parseDRActionCount(drpc)

	if counts.LastPhase == phase {
		return false
	}

	// Only transitions into or out of Initiating are recorded
	if counts.LastPhase != rmn.Initiating && phase != rmn.Initiating {
		return false
	}

	if counts.LastPhase == rmn.Initiating {
		// Initiating into any other phase only disarms
		//nolint:exhaustive
		switch phase {
		case rmn.FailingOver:
			counts.Failover++
		case rmn.Relocating:
			counts.Relocate++
		}
	}

	counts.LastPhase = phase

	value, err := json.Marshal(counts)
	if err != nil {
		return false
	}

	return rmnutil.AddAnnotation(drpc, DRActionCountAnnotation, string(value))
}

// drpcActionCounts returns the cumulative failover and relocate action counts
// persisted on a DRPC
func drpcActionCounts(drpc *rmn.DRPlacementControl) (float64, float64) {
	counts := parseDRActionCount(drpc)

	return float64(counts.Failover), float64(counts.Relocate)
}

// drPolicyDRType classifies a DRPolicy as DRTypeMetro, DRTypeRegional or
// DRTypeUnknown for the ramen_dr_policy_type telemetry metric. It reuses
// dRPolicySupportsMetro so that the classification matches Ramen's
// operational Metro detection: status peerClasses when present, with a
// fallback to spec.schedulingInterval for policies without peerClasses.
func drPolicyDRType(drpolicy *rmn.DRPolicy) string {
	metro, _, err := dRPolicySupportsMetro(drpolicy, nil)
	if err != nil {
		return DRTypeUnknown
	}

	if metro {
		return DRTypeMetro
	}

	return DRTypeRegional
}
