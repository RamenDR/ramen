// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

// reconcileNonConsistencyGroupDeprecation records that VolumeReplication without
// consistency groups is still in use, and removes that warning once it is not.
// The event is emitted on the transition into that state and then at most once
// per NonConsistencyGroupDeprecatedEventInterval for this object.
func (v *VRGInstance) reconcileNonConsistencyGroupDeprecation(now time.Time) {
	applies := v.nonConsistencyGroupVolumeReplicationInUse()
	cleared := setNonConsistencyGroupDeprecationCondition(
		&v.instance.Status.Conditions,
		VRGConditionTypeNonConsistencyGroupDeprecated,
		v.instance.Generation,
		applies,
	)

	var recorder *util.EventReporter

	if v.reconciler != nil {
		recorder = v.reconciler.eventRecorder
	}

	reportDeprecation(recorder, v.instance, v.log, applies, cleared, now)
}

// reconcileDRPCNonConsistencyGroupDeprecation mirrors the VolumeReplicationGroup
// warning onto the DRPlacementControl. It follows the VRG condition, so a
// consistency-group workload does not warn just because protection is enabled.
func (r *DRPlacementControlReconciler) reconcileDRPCNonConsistencyGroupDeprecation(
	drpc *rmn.DRPlacementControl, vrg *rmn.VolumeReplicationGroup, log logr.Logger, now time.Time,
) {
	applies := vrgHasNonConsistencyGroupDeprecation(vrg)
	cleared := setNonConsistencyGroupDeprecationCondition(
		&drpc.Status.Conditions,
		rmn.ConditionNonConsistencyGroupDeprecated,
		drpc.Generation,
		applies,
	)

	reportDeprecation(r.eventRecorder, drpc, log, applies, cleared, now)
}

func reportDeprecation(
	recorder *util.EventReporter, obj runtime.Object, log logr.Logger, applies, cleared bool, now time.Time,
) {
	if reportNonConsistencyGroupDeprecation(recorder, obj, applies, now) {
		log.Info("Emitted deprecation warning for VolumeReplication without consistency groups")
	}

	if cleared {
		log.Info("Cleared VolumeReplication without consistency groups deprecation condition")
	}
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

	condition := meta.FindStatusCondition(vrg.Status.Conditions, VRGConditionTypeNonConsistencyGroupDeprecated)

	return condition != nil && condition.Status == metav1.ConditionTrue
}

// setNonConsistencyGroupDeprecationCondition adds the warning condition while it
// applies and removes it otherwise. Removal is how the warning disappears from
// status once consistency groups are in use. LastTransitionTime is left unchanged
// while the condition stays True.
func setNonConsistencyGroupDeprecationCondition(
	conditions *[]metav1.Condition, conditionType string, observedGeneration int64, applies bool,
) bool {
	if !applies {
		return meta.RemoveStatusCondition(conditions, conditionType)
	}

	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               conditionType,
		Status:             metav1.ConditionTrue,
		Reason:             util.EventReasonNonConsistencyGroupDeprecated,
		Message:            util.NonConsistencyGroupDeprecatedMessage,
		ObservedGeneration: observedGeneration,
	})

	return false
}

func reportNonConsistencyGroupDeprecation(
	recorder *util.EventReporter, obj runtime.Object, applies bool, now time.Time,
) bool {
	if !applies {
		util.ForgetInterval(recorder, obj, util.EventReasonNonConsistencyGroupDeprecated)

		return false
	}

	return util.ReportAtInterval(
		recorder,
		obj,
		corev1.EventTypeWarning,
		util.EventReasonNonConsistencyGroupDeprecated,
		util.NonConsistencyGroupDeprecatedMessage,
		util.NonConsistencyGroupDeprecatedEventInterval,
		now,
	)
}
