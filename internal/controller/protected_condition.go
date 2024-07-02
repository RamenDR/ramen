// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func updateProtectedConditionUnknown(drpc *rmn.DRPlacementControl, clusterName string) {
	addOrUpdateCondition(
		&drpc.Status.Conditions,
		rmn.ConditionProtected,
		drpc.Generation,
		metav1.ConditionUnknown,
		rmn.ReasonProtectedUnknown,
		fmt.Sprintf("Missing VolumeReplicationGroup status from cluster %s", clusterName))
}

// updateDRPCProtectedCondition updates the DRPC status condition Protected based on various states of VRG, from the
// cluster where the workload is expected to be placed. The VRG passed in should be the one where the workload is
// currently deployed. e.g Primary, or in cases where we are waiting for VRG to report Secondary during Relocate
func updateDRPCProtectedCondition(
	drpc *rmn.DRPlacementControl,
	vrg *rmn.VolumeReplicationGroup,
	clusterName string,
) {
	if updateVRGClusterDataReady(drpc, vrg, clusterName) {
		return
	}

	switch vrg.Spec.ReplicationState {
	case rmn.Primary:
		if updateVRGDataReadyAsPrimary(drpc, vrg, clusterName) {
			return
		}

		if updateVRGDataProtectedAsPrimary(drpc, vrg, clusterName) {
			return
		}
	case rmn.Secondary:
		if updateVRGDataReadyAsSecondary(drpc, vrg, clusterName) {
			return
		}

		if updateVRGDataProtectedAsSecondary(drpc, vrg, clusterName) {
			return
		}
	}

	if updateMiscVRGStatus(drpc, vrg, clusterName) {
		return
	}

	// ClusterDataProtected goes last, as this may always report true on Failover when one of the peer clusters is down
	// and hence mask other failures above.
	if updateVRGClusterDataProtected(drpc, vrg, clusterName) {
		return
	}

	addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionProtected, drpc.Generation,
		metav1.ConditionTrue,
		rmn.ReasonProtected,
		fmt.Sprintf("VolumeReplicationGroup (%s/%s) on cluster %s is protecting required resources and data",
			vrg.GetNamespace(), vrg.GetName(), clusterName))
}

// updateVRGClusterDataReady is a helper function to process VRG ClusterDataReady condition and update DRPC
// Protected condition.
//   - Returns a bool that is true if status was updated, and false otherwise
func updateVRGClusterDataReady(drpc *rmn.DRPlacementControl,
	vrg *rmn.VolumeReplicationGroup,
	clusterName string,
) bool {
	updated := true

	// ClusterDataReady is only reported when VRG is Primary
	if vrg.Spec.ReplicationState != rmn.Primary {
		return !updated
	}

	return genericUpdateProtectedForCondition(drpc, vrg, clusterName, VRGConditionTypeClusterDataReady,
		"workload resources readiness", "restoring workload resources", "restoring workload resources")
}

// updateVRGClusterDataProtected is a helper function to process VRG ClusterDataProtected condition and update DRPC
// Protected condition.
//   - Returns a bool that is true if status was updated, and false otherwise
func updateVRGClusterDataProtected(drpc *rmn.DRPlacementControl,
	vrg *rmn.VolumeReplicationGroup,
	clusterName string,
) bool {
	updated := true

	// ClusterDataProtected is only reported when VRG is Primary
	if vrg.Spec.ReplicationState != rmn.Primary {
		return !updated
	}

	return genericUpdateProtectedForCondition(drpc, vrg, clusterName, VRGConditionTypeClusterDataProtected,
		"workload resource protection", "protecting workload resources", "protecting workload resources")
}

// updateVRGDataReadyAsPrimary is a helper function to process VRG DataReady when VRG is Primary and update DRPC
// Protected condition
//   - Returns a bool that is true if status was updated, and false otherwise
func updateVRGDataReadyAsPrimary(drpc *rmn.DRPlacementControl,
	vrg *rmn.VolumeReplicationGroup,
	clusterName string,
) bool {
	return genericUpdateProtectedForCondition(drpc, vrg, clusterName, VRGConditionTypeDataReady,
		"workload data readiness", "readying workload data", "readying workload data")
}

// updateVRGDataReadyAsSecondary is a helper function to process VRG DataReady when VRG is Secondary and update DRPC
// Protected condition
//   - Returns a bool that is true if status was updated, and false otherwise
func updateVRGDataReadyAsSecondary(drpc *rmn.DRPlacementControl,
	vrg *rmn.VolumeReplicationGroup,
	clusterName string,
) bool {
	updated := true

	condition := meta.FindStatusCondition(vrg.Status.Conditions, VRGConditionTypeDataReady)

	// Volsync does not report in a DataReady condition as Secondary
	if condition == nil {
		return !updated
	}

	// NOTE: the check for reason Replicating is only a safety, a Secondary VRG with Failover action would not be
	// used to provide Protected status (a Secondary VRG with Relocate may though, but in that case we want
	// this to be true)
	if condition.ObservedGeneration == vrg.Generation && condition.Status == metav1.ConditionFalse &&
		condition.Reason == VRGConditionReasonReplicating && vrg.Spec.Async != nil &&
		vrg.Spec.Action == rmn.VRGActionFailover {
		return !updated
	}

	return genericUpdateProtectedForCondition(drpc, vrg, clusterName, VRGConditionTypeDataReady,
		"workload data readiness", "readying workload data", "readying workload data")
}

// updateVRGDataProtectedAsPrimary is a helper function to process VRG DataProtected when VRG is Primary and update DRPC
// Protected condition
//   - Returns a bool that is true if status was updated, and false otherwise
func updateVRGDataProtectedAsPrimary(drpc *rmn.DRPlacementControl,
	vrg *rmn.VolumeReplicationGroup,
	clusterName string,
) bool {
	updated := true

	condition := meta.FindStatusCondition(vrg.Status.Conditions, VRGConditionTypeDataProtected)

	if condition != nil && condition.ObservedGeneration == vrg.Generation {
		// VRGConditionReasonReplicating reason is unique to VR based volumes
		if condition.Reason == VRGConditionReasonReplicating && condition.Status == metav1.ConditionFalse {
			return !updated
		}

		if condition.Status == metav1.ConditionTrue {
			return !updated
		}
	}

	return genericUpdateProtectedForCondition(drpc, vrg, clusterName, VRGConditionTypeDataProtected,
		"workload data protection", "protecting workload data", "protecting workload data")
}

// updateVRGDataProtectedAsSecondary is a helper function to process VRG DataProtected when VRG is Secondary and update
// DRPC Protected condition
//   - Returns a bool that is true if status was updated, and false otherwise
func updateVRGDataProtectedAsSecondary(drpc *rmn.DRPlacementControl,
	vrg *rmn.VolumeReplicationGroup,
	clusterName string,
) bool {
	updated := true

	condition := meta.FindStatusCondition(vrg.Status.Conditions, VRGConditionTypeDataProtected)

	// Volsync does not report in a DataReady condition as Secondary
	if condition == nil {
		return !updated
	}

	return genericUpdateProtectedForCondition(drpc, vrg, clusterName, VRGConditionTypeDataProtected,
		"workload data protection", "protecting workload data", "protecting workload data")
}

// genericUpdateProtectedForCondition is a common helper that processes passed in VRG condition with varying VRG reasons
// to determine DRPC Protected condition updates,
func genericUpdateProtectedForCondition(drpc *rmn.DRPlacementControl,
	vrg *rmn.VolumeReplicationGroup,
	clusterName string,
	conditionName string,
	msgUnknown, msgProgressing, msgError string,
) bool {
	updated := true

	condition := meta.FindStatusCondition(vrg.Status.Conditions, conditionName)

	if condition != nil && condition.Status == metav1.ConditionTrue && condition.ObservedGeneration == vrg.Generation {
		return !updated
	}

	if condition == nil ||
		condition.ObservedGeneration != vrg.Generation ||
		condition.Status == metav1.ConditionUnknown {
		addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionProtected, drpc.Generation,
			metav1.ConditionUnknown,
			rmn.ReasonProtectedUnknown,
			fmt.Sprintf("VolumeReplicationGroup (%s/%s) on cluster %s "+
				"is not reporting any status about %s, "+
				"retrying till %s condition is met",
				vrg.GetNamespace(), vrg.GetName(),
				clusterName, msgUnknown, conditionName))

		return updated
	}

	// condition.Status == metav1.ConditionFalse for current generation, other states are exhausted above
	if isVRGReasonError(condition) {
		addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionProtected, drpc.Generation,
			metav1.ConditionFalse,
			rmn.ReasonProtectedError,
			fmt.Sprintf("VolumeReplicationGroup (%s/%s) on cluster %s is reporting errors (%s) %s, "+
				"retrying till %s condition is met",
				vrg.GetNamespace(), vrg.GetName(),
				clusterName, condition.Message, msgError, conditionName))

		return updated
	}

	addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionProtected, drpc.Generation,
		metav1.ConditionFalse,
		rmn.ReasonProtectedProgressing,
		fmt.Sprintf("VolumeReplicationGroup (%s/%s) on cluster %s is progressing on %s (%s), "+
			"retrying till %s condition is met",
			vrg.GetNamespace(), vrg.GetName(),
			clusterName, msgProgressing, condition.Message, conditionName))

	return updated
}

// updateMiscVRGStatus processes VRG status fields other than conditions to determine DRPC Protected condition updates
func updateMiscVRGStatus(drpc *rmn.DRPlacementControl,
	vrg *rmn.VolumeReplicationGroup,
	clusterName string,
) bool {
	updated := true

	if vrg.Status.ObservedGeneration != vrg.Generation {
		addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionProtected, drpc.Generation, metav1.ConditionFalse,
			rmn.ReasonProtectedUnknown, fmt.Sprintf("VolumeReplicationGroup (%s/%s) on cluster %s "+
				"is not reporting status for current generation as %s, retrying till status is met",
				vrg.GetNamespace(), vrg.GetName(),
				clusterName, vrg.Spec.ReplicationState))

		return updated
	}

	if vrg.Status.State != getStatusStateFromSpecState(vrg.Spec.ReplicationState) {
		addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionProtected, drpc.Generation, metav1.ConditionFalse,
			rmn.ReasonProtectedProgressing, fmt.Sprintf("VolumeReplicationGroup (%s/%s) on cluster %s "+
				"is not reporting status as %s, retrying till status is met",
				vrg.GetNamespace(), vrg.GetName(),
				clusterName, vrg.Spec.ReplicationState))

		return updated
	}

	if vrg.Spec.Async != nil && vrg.Status.LastGroupSyncTime.IsZero() {
		addOrUpdateCondition(&drpc.Status.Conditions, rmn.ConditionProtected, drpc.Generation, metav1.ConditionFalse,
			rmn.ReasonProtectedProgressing, fmt.Sprintf("VolumeReplicationGroup (%s/%s) on cluster %s "+
				"is not reporting any lastGroupSyncTime as %s, retrying till status is met",
				vrg.GetNamespace(), vrg.GetName(),
				clusterName, vrg.Spec.ReplicationState))

		return updated
	}

	return !updated
}
