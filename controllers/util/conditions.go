// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"time"

	"github.com/go-logr/logr"
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

func ConditionSetFirstFalseOrLastTrue(
	conditionSet func(*[]metav1.Condition, metav1.Condition),
	conditions *[]metav1.Condition,
	subConditions ...*metav1.Condition,
) {
	trueSubConditions := make([]*metav1.Condition, 0, len(subConditions))

	for _, subCondition := range subConditions {
		if subCondition == nil {
			continue
		}

		if subCondition.Status == metav1.ConditionFalse {
			conditionSet(conditions, *subCondition)

			return
		}

		trueSubConditions = append(trueSubConditions, subCondition)
	}

	if len(trueSubConditions) > 0 {
		conditionSet(conditions, *trueSubConditions[len(trueSubConditions)-1])
	}
}
