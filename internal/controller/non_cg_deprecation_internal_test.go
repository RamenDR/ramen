// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

var _ = Describe("NonConsistencyGroupDeprecation", func() {
	It("sets DeprecatedWarnings and leaves LastTransitionTime unchanged", func() {
		var conditions []metav1.Condition

		setNonConsistencyGroupDeprecationCondition(&conditions, 3)

		condition := nonCGDeprecationConditionExpect(conditions, 3)
		old := metav1.NewTime(time.Unix(1000, 0).UTC())
		condition.LastTransitionTime = old

		setNonConsistencyGroupDeprecationCondition(&conditions, 4)

		condition = nonCGDeprecationConditionExpect(conditions, 4)
		Expect(condition.LastTransitionTime).To(Equal(old))
	})

	It("clears DeprecatedWarnings", func() {
		var conditions []metav1.Condition

		setNonConsistencyGroupDeprecationCondition(&conditions, 3)

		Expect(removeNonConsistencyGroupDeprecation(&conditions)).To(BeTrue())
		Expect(meta.FindStatusCondition(conditions, VRGConditionTypeDeprecatedWarnings)).To(BeNil())
	})

	It("leaves a DeprecatedWarnings condition with a different reason", func() {
		conditions := []metav1.Condition{{
			Type:    VRGConditionTypeDeprecatedWarnings,
			Status:  metav1.ConditionTrue,
			Reason:  "FutureDeprecation",
			Message: "some later warning",
		}}

		Expect(removeNonConsistencyGroupDeprecation(&conditions)).To(BeFalse())

		condition := meta.FindStatusCondition(conditions, VRGConditionTypeDeprecatedWarnings)
		Expect(condition).ToNot(BeNil())
		Expect(condition.Reason).To(Equal("FutureDeprecation"))
	})

	It("reports only the non-consistency-group warning", func() {
		Expect(vrgHasNonConsistencyGroupDeprecation(nil)).To(BeFalse())

		vrg := &rmn.VolumeReplicationGroup{}
		Expect(vrgHasNonConsistencyGroupDeprecation(vrg)).To(BeFalse())

		meta.SetStatusCondition(&vrg.Status.Conditions, metav1.Condition{
			Type:    VRGConditionTypeDeprecatedWarnings,
			Status:  metav1.ConditionTrue,
			Reason:  "FutureDeprecation",
			Message: "other",
		})
		Expect(vrgHasNonConsistencyGroupDeprecation(vrg)).To(BeFalse())

		meta.SetStatusCondition(&vrg.Status.Conditions, metav1.Condition{
			Type:    VRGConditionTypeDeprecatedWarnings,
			Status:  metav1.ConditionTrue,
			Reason:  util.EventReasonNonConsistencyGroupDeprecated,
			Message: util.NonConsistencyGroupDeprecatedMessage,
		})
		Expect(vrgHasNonConsistencyGroupDeprecation(vrg)).To(BeTrue())
	})
})

func nonCGDeprecationConditionExpect(conditions []metav1.Condition, generation int64) *metav1.Condition {
	condition := meta.FindStatusCondition(conditions, VRGConditionTypeDeprecatedWarnings)
	Expect(condition).ToNot(BeNil())
	Expect(condition.Status).To(Equal(metav1.ConditionTrue))
	Expect(condition.Reason).To(Equal(util.EventReasonNonConsistencyGroupDeprecated))
	Expect(condition.ObservedGeneration).To(Equal(generation))
	Expect(condition.Message).To(Equal(util.NonConsistencyGroupDeprecatedMessage))

	return condition
}
