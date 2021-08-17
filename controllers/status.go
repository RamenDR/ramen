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

// VRG condition types
const (
	VRGConditionAvailable = "Available"
)

// Protected PVC condition types
const (
	PVCConditionAvailable = "Available"
)

const (
	PVConditionMetadataAvailable = "MetadataAvailable"
)

const (
	VRGReplicating = "Replicating"
	VRGProgressing = "Progressing"
	VRGError       = "Error"
)

const (
	PVCReplicating  = "Replicating"
	PVCProgressing  = "Progressing"
	PVCError        = "Error"
	PVCErrorUnknown = "UnknownError"
)

const (
	PVMetadataRestored    = "Restored"
	PVMetadataProgressing = "Progressing"
	PVMetadataError       = "Error"
)

// Just when VRG has been picked up for reconciliation when nothing has been
// figured out yet.
func setVRGInitialCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionAvailable,
		Reason:             "none",
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
}

// sets conditions when VRG is replicating
func setVRGReplicatingCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionAvailable,
		Reason:             VRGReplicating,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
}

// sets conditions when VRG is progressing
func setVRGProgressingCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionAvailable,
		Reason:             VRGProgressing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

// sets conditions when VRG sees failures in data sync
func setVRGErrorCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               VRGConditionAvailable,
		Reason:             VRGError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

// sets conditions when PVC data is replicating
func setPVCReplicatingCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               PVCConditionAvailable,
		Reason:             PVCReplicating,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
}

// sets conditions when VolumeReplication resource creation
// request has been just made for the PVC.
func setPVCProgressingCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               PVCConditionAvailable,
		Reason:             PVCProgressing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

// sets conditions when VolumeReplication resource
// associated with the PVC is not replicating due
// some problem.
func setPVCErrorCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               PVCConditionAvailable,
		Reason:             PVCError,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

// sets conditions when VolumeReplicationGroup is unable
// to get the VolumeReplication resource from kube API
// server and it is not known what the current state of
// the VolumeReplication resource (including its existence).
func setPVCErrorUnknownCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               PVCConditionAvailable,
		Reason:             PVCErrorUnknown,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionUnknown,
		Message:            message,
	})
}

// sets conditions when PV meatadata is restored
func setPVMetadataAvailableCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               PVConditionMetadataAvailable,
		Reason:             PVMetadataRestored,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionTrue,
		Message:            message,
	})
}

// sets conditions when PV metadata is being restored
func setPVMetadataProgressingCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               PVConditionMetadataAvailable,
		Reason:             PVMetadataProgressing,
		ObservedGeneration: observedGeneration,
		Status:             metav1.ConditionFalse,
		Message:            message,
	})
}

// sets conditions when PV metadata failed to restore
func setPVMetadataErrorCondition(conditions *[]metav1.Condition, observedGeneration int64, message string) {
	setStatusCondition(conditions, metav1.Condition{
		Type:               PVConditionMetadataAvailable,
		Reason:             PVMetadataError,
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
