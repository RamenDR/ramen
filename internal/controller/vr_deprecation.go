// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"slices"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

// reconcileVRGNonConsistencyGroupDeprecation records that VolumeReplication without
// consistency groups is still in use, and removes that warning once it is not.
// Other warnings on this VolumeReplicationGroup stay. The warning event is
// emitted on this VolumeReplicationGroup at most once per
// DepracatedWarningEventInterval.
func (v *VRGInstance) reconcileVRGNonConsistencyGroupDeprecation() {
	if !v.nonConsistencyGroupVolumeReplicationInUse() {
		removeWarning(v.log, &v.instance.Status.Warnings, rmn.WarningNonConsistencyGroupDeprecated)

		return
	}

	setWarning(
		v.log,
		&v.instance.Status.Warnings,
		rmn.WarningNonConsistencyGroupDeprecated,
		util.VolumeReplicationDeprecatedMessage,
	)
	reportNonConsistencyGroupDeprecation(v.reconciler.eventRecorder, v.instance)
}

// reconcileDRPCNonConsistencyGroupDeprecation mirrors this VolumeReplicationGroup
// warning onto the DRPlacementControl. It follows the VRG warning, so a
// consistency-group workload does not warn just because protection is enabled.
// Other warnings on the DRPlacementControl stay.
func (r *DRPlacementControlReconciler) reconcileDRPCNonConsistencyGroupDeprecation(
	drpc *rmn.DRPlacementControl, vrg *rmn.VolumeReplicationGroup, log logr.Logger,
) {
	if !vrgHasNonConsistencyGroupDeprecation(vrg) {
		removeWarning(log, &drpc.Status.Warnings, rmn.WarningNonConsistencyGroupDeprecated)

		return
	}

	warning, _ := findWarning(vrg.Status.Warnings, rmn.WarningNonConsistencyGroupDeprecated)
	setWarning(log, &drpc.Status.Warnings, warning.Name, warning.Message)
	reportNonConsistencyGroupDeprecation(r.eventRecorder, drpc)
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

	_, found := findWarning(vrg.Status.Warnings, rmn.WarningNonConsistencyGroupDeprecated)

	return found
}

// setWarning records name on the list. An existing entry keeps its position
// and receives message. A changed message is logged. Other warnings stay.
func setWarning(log logr.Logger, warnings *[]rmn.Warning, name, message string) {
	// A nil pointer has no warning list to update. A nil slice is still a list
	// and can grow.
	if warnings == nil {
		return
	}

	// Look for this warning name. An existing entry keeps its place. The message
	// is written and logged only when it changed. A missing name is appended below.
	for i := range *warnings {
		if (*warnings)[i].Name == name {
			if (*warnings)[i].Message == message {
				return
			}

			(*warnings)[i].Message = message

			log.Info(
				"Updated VolumeReplication without consistency groups deprecation warning message",
				"message", message,
			)

			return
		}
	}

	*warnings = append(*warnings, rmn.Warning{Name: name, Message: message})
}

// removeWarning removes the warning with this name from the status list.
// It logs the cleared warning. Other warnings stay. When no warnings remain,
// the list is set to nil so status omits it.
//
//nolint:unparam // name stays a parameter so later warnings can use this helper
func removeWarning(log logr.Logger, warnings *[]rmn.Warning, name string) {
	if warnings == nil || len(*warnings) == 0 {
		return
	}

	i := slices.IndexFunc(*warnings, func(warning rmn.Warning) bool {
		return warning.Name == name
	})
	if i < 0 {
		return
	}

	log.Info(
		"Cleared VolumeReplication without consistency groups deprecation warning",
		"message", (*warnings)[i].Message,
	)

	*warnings = slices.Delete(*warnings, i, i+1)
	if len(*warnings) == 0 {
		*warnings = nil
	}
}

func findWarning(warnings []rmn.Warning, name string) (rmn.Warning, bool) {
	for _, warning := range warnings {
		if warning.Name == name {
			return warning, true
		}
	}

	return rmn.Warning{}, false
}

// reportNonConsistencyGroupDeprecation emits the warning on instance. The
// caller has already decided that VolumeReplication without consistency groups
// is in use.
func reportNonConsistencyGroupDeprecation(recorder *util.EventReporter, instance runtime.Object) {
	util.ReportIfNotPresent(
		recorder,
		instance,
		corev1.EventTypeWarning,
		util.EventReasonDeprecatedWarning,
		util.VolumeReplicationDeprecatedMessage,
		util.DepracatedWarningEventInterval,
	)
}
