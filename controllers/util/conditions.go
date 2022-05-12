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
	log logr.Logger) bool {
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
