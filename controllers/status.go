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
)

// VRG condition reasons
const (
	VRGConditionReasonInitializing        = "Initializing"
	VRGConditionReasonReplicating         = "Replicating"
	VRGConditionReasonProgressing         = "Progressing"
	VRGConditionReasonClusterDataRestored = "Restored"
	VRGConditionReasonError               = "Error"
	VRGConditionReasonErrorUnknown        = "UnknownError"
	VRGConditionReasonUploading           = "Uploading"
	VRGConditionReasonUploaded            = "Uploaded"
	VRGConditionReasonUploadFailed        = "UploadFailed"
	VRGConditionReasonMissingS3Profile    = "MissingS3Profile"
)

// Just when VRG has been picked up for reconciliation when nothing has been
// figured out yet.
func setVRGInitialCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeDataReady,
		Reason:             VRGConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeClusterDataReady,
		Reason:             VRGConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeClusterDataProtected,
		Reason:             VRGConditionReasonInitializing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
}

// sets conditions when VRG is data replicating
func setVRGDataReplicatingCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeDataReady,
		Reason:             VRGConditionReasonReplicating,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
}

// sets conditions when VRG data is progressing
func setVRGDataProgressingCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeDataReady,
		Reason:             VRGConditionReasonProgressing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

// sets conditions when VRG sees failures in data sync
func setVRGDataErrorCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeDataReady,
		Reason:             VRGConditionReasonError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
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

// sets conditions when PV cluster data is being restored
func setVRGClusterDataProgressingCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeClusterDataReady,
		Reason:             VRGConditionReasonProgressing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
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
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeClusterDataProtected,
		Reason:             VRGConditionReasonUploaded,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
}

// sets conditions when PV cluster data is being protected
func setVRGClusterDataProtectingCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeClusterDataProtected,
		Reason:             VRGConditionReasonUploading,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

// sets conditions when PV cluster data failed to be protected
func setVRGClusterDataUnprotectedCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionTypeClusterDataProtected,
		Reason:             VRGConditionReasonUploadFailed,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
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
