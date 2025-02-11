// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/exp/slices"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GenericStatusConditionSet(
	object client.Object,
	conditions *[]metav1.Condition,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
	log logr.Logger,
) bool {
	updated := true
	generation := object.GetGeneration()

	if condition := meta.FindStatusCondition(*conditions, conditionType); condition != nil {
		if condition.Status == status &&
			condition.Reason == reason &&
			condition.Message == message &&
			condition.ObservedGeneration == generation {
			log.Info("condition unchanged", "type", conditionType,
				"status", status, "reason", reason, "message", message, "generation", generation,
			)

			return !updated
		}

		log.Info("condition update", "type", conditionType,
			"old status", condition.Status, "new status", status,
			"old reason", condition.Reason, "new reason", reason,
			"old message", condition.Message, "new message", message,
			"old generation", condition.ObservedGeneration, "new generation", generation,
		)
		ConditionUpdate(object, condition, status, reason, message)
	} else {
		log.Info("condition append", "type", conditionType,
			"status", status, "reason", reason, "message", message, "generation", generation,
		)
		ConditionAppend(object, conditions, conditionType, status, reason, message)
	}

	return updated
}

func ConditionUpdate(
	object metav1.Object,
	condition *metav1.Condition,
	status metav1.ConditionStatus,
	reason,
	message string,
) {
	condition.Status = status
	condition.ObservedGeneration = object.GetGeneration()
	condition.LastTransitionTime = metav1.NewTime(time.Now())
	condition.Reason = reason
	condition.Message = message
}

func ConditionAppend(
	object metav1.Object,
	conditions *[]metav1.Condition,
	conditionType string,
	status metav1.ConditionStatus,
	reason,
	message string,
) {
	*conditions = append(
		*conditions,
		metav1.Condition{
			Type:               conditionType,
			Status:             status,
			ObservedGeneration: object.GetGeneration(),
			LastTransitionTime: metav1.NewTime(time.Now()),
			Reason:             reason,
			Message:            message,
		},
	)
}

// MergeConditions merges VRG conditions of the same type to generate a single condition for the Type
func MergeConditions(
	conditionSet func(*[]metav1.Condition, metav1.Condition) metav1.Condition,
	conditions *[]metav1.Condition,
	ignoreReasons []string,
	subConditions ...*metav1.Condition,
) metav1.Condition {
	trueSubConditions := []*metav1.Condition{}
	falseSubConditions := []*metav1.Condition{}
	unknownSubConditions := []*metav1.Condition{}

	for _, subCondition := range subConditions {
		if subCondition == nil {
			continue
		}

		switch subCondition.Status {
		case metav1.ConditionFalse:
			falseSubConditions = oldestConditions(subCondition, falseSubConditions)
		case metav1.ConditionUnknown:
			unknownSubConditions = oldestConditions(subCondition, unknownSubConditions)
		case metav1.ConditionTrue:
			trueSubConditions = oldestConditions(subCondition, trueSubConditions)
		}
	}

	var finalCondition metav1.Condition

	switch {
	case len(falseSubConditions) != 0:
		finalCondition = conditionSet(conditions, mergedCondition(falseSubConditions, ignoreReasons))
	case len(unknownSubConditions) != 0:
		finalCondition = conditionSet(conditions, mergedCondition(unknownSubConditions, ignoreReasons))
	case len(trueSubConditions) != 0:
		finalCondition = conditionSet(conditions, mergedCondition(trueSubConditions, ignoreReasons))
	}

	return finalCondition
}

// oldestConditions returns a list of conditions that are the same generation and the oldest among newCondition and
// conditions[]. Initial call should pass an empty conditions[] and iterate over conditions to merge, as the function
// expects conditions in passed in conditions[] to be the of the same generation.
func oldestConditions(newCondition *metav1.Condition, conditions []*metav1.Condition) []*metav1.Condition {
	retConditions := []*metav1.Condition{}

	if len(conditions) == 0 {
		retConditions = append(retConditions, newCondition)

		return retConditions
	}

	// All conditions in conditions[] are from an older generation
	if conditions[0].ObservedGeneration < newCondition.ObservedGeneration {
		return conditions
	}

	// newCondition is of the same generation as conditions in conditions[]
	if conditions[0].ObservedGeneration == newCondition.ObservedGeneration {
		conditions = append(conditions, newCondition)

		return conditions
	}

	// newCondition generation is lower than all conditions in conditions[]
	retConditions = append(retConditions, newCondition)

	return retConditions
}

// mergedCondition merges messages from conditions[] that MUST contain conditions with the same Type,
// ObservedGeneration, Status.
// If a condition in conditions[] has a Reason in ignoreReasons it is ignored in the merge unless all conditions
// have a reason from ignoreReasons
func mergedCondition(conditions []*metav1.Condition, ignoreReasons []string) metav1.Condition {
	merged := metav1.Condition{}

	for _, subCondition := range conditions {
		if merged.Type == "" {
			merged = *subCondition

			continue
		}

		if subCondition.Reason == merged.Reason {
			merged.Message += ". " + subCondition.Message

			continue
		}

		// If current condition has an ignore reason, carry older merged condition as is
		if slices.Contains(ignoreReasons, subCondition.Reason) {
			continue
		}

		// If older merged condition has an ignore reason, carry current condition
		if slices.Contains(ignoreReasons, merged.Reason) {
			merged = *subCondition

			continue
		}

		// NOTE: Reason differs, for now merge the message. We could look at error reasons
		// taking precedence over other reasons
		merged.Message += subCondition.Message
	}

	return merged
}
