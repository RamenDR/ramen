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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
