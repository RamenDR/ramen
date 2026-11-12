// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

// reconcileVRGNonConsistencyGroupDeprecation records that VolumeReplication without
// consistency groups is still in use, and removes that warning once it is not.
// The condition stays on this VolumeReplicationGroup. The warning event is
// controller-wide and is emitted at most once per NonConsistencyGroupDeprecatedEventInterval.
func (v *VRGInstance) reconcileVRGNonConsistencyGroupDeprecation(now time.Time) {
	if !v.nonConsistencyGroupVolumeReplicationInUse() {
		if removeNonConsistencyGroupDeprecation(&v.instance.Status.Conditions) {
			v.log.Info("Cleared VolumeReplication without consistency groups deprecation condition")
		}

		return
	}

	setNonConsistencyGroupDeprecationCondition(&v.instance.Status.Conditions, v.instance.Generation)
	reportNonConsistencyGroupDeprecation(v.reconciler.eventRecorder, v.log, now)
}

// reconcileDRPCNonConsistencyGroupDeprecation mirrors the VolumeReplicationGroup
// warning onto the DRPlacementControl. It follows the VRG condition, so a
// consistency-group workload does not warn just because protection is enabled.
// The warning event is shared by every DRPlacementControl on this controller.
func (r *DRPlacementControlReconciler) reconcileDRPCNonConsistencyGroupDeprecation(
	drpc *rmn.DRPlacementControl, vrg *rmn.VolumeReplicationGroup, log logr.Logger, now time.Time,
) {
	if !vrgHasNonConsistencyGroupDeprecation(vrg) {
		if removeNonConsistencyGroupDeprecation(&drpc.Status.Conditions) {
			log.Info("Cleared VolumeReplication without consistency groups deprecation condition")
		}

		return
	}

	setNonConsistencyGroupDeprecationCondition(&drpc.Status.Conditions, drpc.Generation)
	reportNonConsistencyGroupDeprecation(r.eventRecorder, log, now)
}

// useNonConsistencyGroupVolRep records the PVC classification choice. grouping
// is peerClass.Grouping: false means this StorageClass is replicated with
// individual VolumeReplication.
func (v *VRGInstance) useNonConsistencyGroupVolRep(grouping bool) {
	if !grouping {
		v.selectedNonCGVolRep = true
	}
}

// nonConsistencyGroupVolumeReplicationInUse reports whether PVC classification
// selected individual VolumeReplication for this reconcile.
func (v *VRGInstance) nonConsistencyGroupVolumeReplicationInUse() bool {
	return v.instance.Spec.Async != nil && v.selectedNonCGVolRep
}

func vrgHasNonConsistencyGroupDeprecation(vrg *rmn.VolumeReplicationGroup) bool {
	if vrg == nil {
		return false
	}

	condition := meta.FindStatusCondition(vrg.Status.Conditions, VRGConditionTypeDeprecatedWarnings)

	return condition != nil && condition.Status == metav1.ConditionTrue &&
		condition.Reason == util.EventReasonNonConsistencyGroupDeprecated
}

// setNonConsistencyGroupDeprecationCondition adds DeprecatedWarnings while
// individual VolumeReplication is in use. LastTransitionTime is left unchanged
// while this warning stays True.
func setNonConsistencyGroupDeprecationCondition(conditions *[]metav1.Condition, observedGeneration int64) {
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeDeprecatedWarnings,
		Status:             metav1.ConditionTrue,
		Reason:             util.EventReasonNonConsistencyGroupDeprecated,
		Message:            util.NonConsistencyGroupDeprecatedMessage,
		ObservedGeneration: observedGeneration,
	})
}

// removeNonConsistencyGroupDeprecation removes this warning once consistency
// groups are in use. A DeprecatedWarnings condition with a different reason is
// left in place so another deprecation can share the condition type.
func removeNonConsistencyGroupDeprecation(conditions *[]metav1.Condition) bool {
	if conditions == nil {
		return false
	}

	current := meta.FindStatusCondition(*conditions, VRGConditionTypeDeprecatedWarnings)
	if current == nil || current.Reason != util.EventReasonNonConsistencyGroupDeprecated {
		return false
	}

	return meta.RemoveStatusCondition(conditions, VRGConditionTypeDeprecatedWarnings)
}

// reportNonConsistencyGroupDeprecation emits the controller-wide warning. The
// caller has already decided that VolumeReplication without consistency groups
// is in use.
func reportNonConsistencyGroupDeprecation(recorder *util.EventReporter, log logr.Logger, now time.Time) {
	if util.ReportGlobal(
		recorder,
		corev1.EventTypeWarning,
		util.EventReasonNonConsistencyGroupDeprecated,
		util.NonConsistencyGroupDeprecatedMessage,
		util.NonConsistencyGroupDeprecatedEventInterval,
		now,
	) {
		log.Info("Emitted deprecation warning for VolumeReplication without consistency groups")
	}
}
