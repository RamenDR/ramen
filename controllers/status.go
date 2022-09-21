/*
Copyright 2021 The RamenDR authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VRG status condition types.  These condition are applicable at the VRG
// summary level and at the individual PVC level.  The ClusterDataReady
// condition is only applicable at the VRG summary level and is not a condition
// that applies to individual PVCs.
const (
	// PV data is ready. When failing over or relocating an app to a different
	// cluster, app's PVC will be able to access the storage volume only after
	// this condition is true.
	VRGConditionTypeDataReady = "DataReady"

	// PV data is protected. This means that, the PV data from the storage
	// is in complete sync with its remote peer.
	VRGConditionTypeDataProtected = "DataProtected"

	// PV cluster data is ready.  When failing over or relocating an app to a
	// different cluster, deploying the app prior to ClusterDataReady condition
	// could result in the PVCs binding to newly created PVs instead of binding
	// to their corresponding replicated PVs.  App's PVC should be deployed only
	// after this condition is true.
	VRGConditionTypeClusterDataReady = "ClusterDataReady"

	// PV cluster data is protected.  This condition indicates whether an app,
	// which is active in a cluster, has all its PV related cluster data
	// protected from a disaster by uploading it to the required S3 store(s).
	VRGConditionTypeClusterDataProtected = "ClusterDataProtected"

	// Total number of condition types in VRG as of now. Change this value
	// when a new condition type is added to VRG or an existing condition
	// type is removed from VRG status.
	VRGTotalConditions = 4

	// VolSync related conditions. These conditions are only applicable
	// at individual PVCs and not generic VRG conditions.
	VRGConditionTypeVolSyncRepSourceSetup      = "ReplicationSourceSetup"
	VRGConditionTypeVolSyncFinalSyncInProgress = "FinalSyncInProgress"
	VRGConditionTypeVolSyncRepDestinationSetup = "ReplicationDestinationSetup"
	VRGConditionTypeVolSyncPVsRestored         = "PVsRestored"
)

// VRG condition reasons
const (
	VRGConditionReasonInitializing               = "Initializing"
	VRGConditionReasonReplicating                = "Replicating"
	VRGConditionReasonReplicated                 = "Replicated"
	VRGConditionReasonReady                      = "Ready"
	VRGConditionReasonDataProtected              = "DataProtected"
	VRGConditionReasonProgressing                = "Progressing"
	VRGConditionReasonClusterDataRestored        = "Restored"
	VRGConditionReasonError                      = "Error"
	VRGConditionReasonErrorUnknown               = "UnknownError"
	VRGConditionReasonUploading                  = "Uploading"
	VRGConditionReasonUploaded                   = "Uploaded"
	VRGConditionReasonUploadError                = "UploadError"
	VRGConditionReasonVolSyncRepSourceInited     = "SourceInitialized"
	VRGConditionReasonVolSyncRepDestInited       = "DestinationInitialized"
	VRGConditionReasonVolSyncPVsRestored         = "Restored"
	VRGConditionReasonVolSyncFinalSyncInProgress = "Syncing"
	VRGConditionReasonVolSyncFinalSyncComplete   = "Synced"
)

// Just when VRG has been picked up for reconciliation when nothing has been
// figured out yet.
func setVRGInitialCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	if len(*conditions) == VRGTotalConditions {
		return
	}

	time := metav1.NewTime(time.Now())

	setStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               VRGConditionTypeDataReady,
		Reason:             VRGConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		LastTransitionTime: time,
		Message:            message,
	})
	setStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               VRGConditionTypeDataProtected,
		Reason:             VRGConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		LastTransitionTime: time,
		Message:            message,
	})
	setStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               VRGConditionTypeClusterDataReady,
		Reason:             VRGConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		LastTransitionTime: time,
		Message:            message,
	})
	setStatusConditionIfNotFound(conditions, metav1.Condition{
		Type:               VRGConditionTypeClusterDataProtected,
		Reason:             VRGConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		LastTransitionTime: time,
		Message:            message,
	})
}

// sets conditions when VRG as Secondary is replicating the data with Primary.
func setVRGDataReplicatingCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, *newVRGDataReplicatingCondition(observedGeneration, message))
}

func newVRGDataReplicatingCondition(observedGeneration int64, message string) *metav1.Condition {
	return &metav1.Condition{
		Type:               VRGConditionTypeDataReady,
		Reason:             VRGConditionReasonReplicating,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	}
}

// sets conditions when VRG as Secondary has completed its data sync with Primary.
func setVRGDataReplicatedCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, *newVRGDataReplicatedCondition(observedGeneration, message))
}

func newVRGDataReplicatedCondition(observedGeneration int64, message string) *metav1.Condition {
	return &metav1.Condition{
		Type:               VRGConditionTypeDataReady,
		Reason:             VRGConditionReasonReplicated,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	}
}

// sets conditions when VRG DataProtected is correct which means
// VR reports: Degraded: True and Resync: True
// This is useful when there is a known Primary, for example post failover,
// or even post relocate before VRG deletion on old secondary (as the
// condition may change based on VR catching up to a new primary elsewhere)
// VR reports: Degraded: False and Resync: False
// This is useful when there are no known primaries and a final sync of
// data is complete across secondaries
func setVRGAsDataProtectedCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	// This means, for this VRG (as secondary) data sync has happened
	// with a remote peer. Hence DataProtected is true
	setStatusCondition(conditions, *newVRGAsDataProtectedCondition(observedGeneration, message))
}

func newVRGAsDataProtectedCondition(observedGeneration int64, message string) *metav1.Condition {
	return &metav1.Condition{
		Type:               VRGConditionTypeDataProtected,
		Reason:             VRGConditionReasonDataProtected,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	}
}

func setVRGAsDataNotProtectedCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, *newVRGAsDataNotProtectedCondition(observedGeneration, message))
}

func newVRGAsDataNotProtectedCondition(observedGeneration int64, message string) *metav1.Condition {
	return &metav1.Condition{
		Type:               VRGConditionTypeDataProtected,
		Reason:             VRGConditionReasonError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	}
}

func setVRGDataProtectionProgressCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, *newVRGDataProtectionProgressCondition(observedGeneration, message))
}

func newVRGDataProtectionProgressCondition(observedGeneration int64, message string) *metav1.Condition {
	return &metav1.Condition{
		Type:               VRGConditionTypeDataProtected,
		Reason:             VRGConditionReasonReplicating,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	}
}

// sets conditions when Primary VRG data replication is established
func setVRGAsPrimaryReadyCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, *newVRGAsPrimaryReadyCondition(observedGeneration, message))
}

func newVRGAsPrimaryReadyCondition(observedGeneration int64, message string) *metav1.Condition {
	return &metav1.Condition{
		Type:               VRGConditionTypeDataReady,
		Reason:             VRGConditionReasonReady,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	}
}

// sets conditions when VRG data is progressing
func setVRGDataProgressingCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, *newVRGDataProgressingCondition(observedGeneration, message))
}

func newVRGDataProgressingCondition(observedGeneration int64, message string) *metav1.Condition {
	return &metav1.Condition{
		Type:               VRGConditionTypeDataReady,
		Reason:             VRGConditionReasonProgressing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	}
}

// sets conditions when VRG sees failures in data sync
func setVRGDataErrorCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, *newVRGDataErrorCondition(observedGeneration, message))
}

func newVRGDataErrorCondition(observedGeneration int64, message string) *metav1.Condition {
	return &metav1.Condition{
		Type:               VRGConditionTypeDataReady,
		Reason:             VRGConditionReasonError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	}
}

// sets conditions when VolumeReplicationGroup is unable
// to get the VolumeReplication resource from kube API
// server and it is not known what the current state of
// the VolumeReplication resource (including its existence).
func setVRGDataErrorUnknownCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeDataReady,
		Reason:             VRGConditionReasonErrorUnknown,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
}

// sets conditions when PV cluster data is restored
func setVRGClusterDataReadyCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeClusterDataReady,
		Reason:             VRGConditionReasonClusterDataRestored,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
}

// sets conditions when PV cluster data failed to restore
func setVRGClusterDataErrorCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeClusterDataReady,
		Reason:             VRGConditionReasonError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

// sets conditions when PV cluster data is protected
func setVRGClusterDataProtectedCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, *newVRGClusterDataProtectedCondition(observedGeneration, message))
}

func newVRGClusterDataProtectedCondition(observedGeneration int64, message string) *metav1.Condition {
	return &metav1.Condition{
		Type:               VRGConditionTypeClusterDataProtected,
		Reason:             VRGConditionReasonUploaded,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	}
}

// sets conditions when PV cluster data is being protected
func setVRGClusterDataProtectingCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, *newVRGClusterDataProtectingCondition(observedGeneration, message))
}

func newVRGClusterDataProtectingCondition(observedGeneration int64, message string) *metav1.Condition {
	return &metav1.Condition{
		Type:               VRGConditionTypeClusterDataProtected,
		Reason:             VRGConditionReasonUploading,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	}
}

// sets conditions when PV cluster data failed to be protected
func setVRGClusterDataUnprotectedCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, *newVRGClusterDataUnprotectedCondition(observedGeneration, message))
}

func newVRGClusterDataUnprotectedCondition(observedGeneration int64, message string) *metav1.Condition {
	return &metav1.Condition{
		Type:               VRGConditionTypeClusterDataProtected,
		Reason:             VRGConditionReasonUploadError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	}
}

func setStatusConditionIfNotFound(existingConditions *[]metav1.Condition, newCondition metav1.Condition) {
	if existingConditions == nil {
		existingConditions = &[]metav1.Condition{}
	}

	existingCondition := findCondition(*existingConditions, newCondition.Type)
	if existingCondition == nil {
		newCondition.LastTransitionTime = metav1.NewTime(time.Now())
		*existingConditions = append(*existingConditions, newCondition)

		return
	}
}

func setStatusCondition(existingConditions *[]metav1.Condition, newCondition metav1.Condition) {
	if existingConditions == nil {
		existingConditions = &[]metav1.Condition{}
	}

	existingCondition := findCondition(*existingConditions, newCondition.Type)
	if existingCondition == nil {
		newCondition.LastTransitionTime = metav1.NewTime(time.Now())
		*existingConditions = append(*existingConditions, newCondition)

		return
	}

	if existingCondition.Status != newCondition.Status {
		existingCondition.Status = newCondition.Status
		existingCondition.LastTransitionTime = metav1.NewTime(time.Now())
	}

	existingCondition.Reason = newCondition.Reason
	existingCondition.Message = newCondition.Message

	if existingCondition.ObservedGeneration != newCondition.ObservedGeneration {
		existingCondition.ObservedGeneration = newCondition.ObservedGeneration
		existingCondition.LastTransitionTime = metav1.NewTime(time.Now())
	}
}

func findCondition(existingConditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range existingConditions {
		if existingConditions[i].Type == conditionType {
			return &existingConditions[i]
		}
	}

	return nil
}

// sets conditions when Primary VolSync has finished setting up the Replication Source
func setVRGConditionTypeVolSyncRepSourceSetupComplete(conditions *[]metav1.Condition, observedGeneration int64,
	message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeVolSyncRepSourceSetup,
		Reason:             VRGConditionReasonVolSyncRepSourceInited,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
}

// sets conditions when Primary anccountered an error initializing the Replication Source
func setVRGConditionTypeVolSyncRepSourceSetupError(conditions *[]metav1.Condition, observedGeneration int64,
	message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeVolSyncRepSourceSetup,
		Reason:             VRGConditionReasonError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

// sets conditions when Primary VolSync has finished setting up the Replication Destination
func setVRGConditionTypeVolSyncPVRestoreComplete(conditions *[]metav1.Condition, observedGeneration int64,
	message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeVolSyncPVsRestored,
		Reason:             VRGConditionReasonVolSyncPVsRestored,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
}

// sets conditions when Primary anccountered an error initializing the Replication Destination
func setVRGConditionTypeVolSyncPVRestoreError(conditions *[]metav1.Condition, observedGeneration int64,
	message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeVolSyncPVsRestored,
		Reason:             VRGConditionReasonError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}
