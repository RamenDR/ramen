// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

func TestDeprecatedWarningsConditionSet(t *testing.T) {
	t.Parallel()

	var conditions []metav1.Condition

	if setNonConsistencyGroupDeprecationCondition(&conditions, 3, true) {
		t.Fatal("setting the warning should not report it as cleared")
	}

	condition := requireNonCGDeprecation(t, conditions, 3)
	old := metav1.NewTime(time.Unix(1_000, 0).UTC())
	condition.LastTransitionTime = old

	if setNonConsistencyGroupDeprecationCondition(&conditions, 4, true) {
		t.Fatal("setting the same warning again should not report it as cleared")
	}

	condition = requireNonCGDeprecation(t, conditions, 4)
	if !condition.LastTransitionTime.Equal(&old) {
		t.Fatalf("expected LastTransitionTime to stay %v, got %v", old, condition.LastTransitionTime)
	}
}

func TestDeprecatedWarningsConditionClear(t *testing.T) {
	t.Parallel()

	var conditions []metav1.Condition

	setNonConsistencyGroupDeprecationCondition(&conditions, 3, true)

	if !setNonConsistencyGroupDeprecationCondition(&conditions, 3, false) {
		t.Fatal("expected the warning to be cleared")
	}

	if meta.FindStatusCondition(conditions, VRGConditionTypeDeprecatedWarnings) != nil {
		t.Fatal("expected DeprecatedWarnings to be removed")
	}
}

func requireNonCGDeprecation(t *testing.T, conditions []metav1.Condition, generation int64) *metav1.Condition {
	t.Helper()

	condition := meta.FindStatusCondition(conditions, VRGConditionTypeDeprecatedWarnings)
	if condition == nil {
		t.Fatal("expected DeprecatedWarnings")
	}

	if condition.Status != metav1.ConditionTrue ||
		condition.Reason != util.EventReasonNonConsistencyGroupDeprecated ||
		condition.ObservedGeneration != generation ||
		condition.Message != util.NonConsistencyGroupDeprecatedMessage {
		t.Fatalf("unexpected condition: %#v", condition)
	}

	return condition
}

func TestDeprecatedWarningsLeavesOtherReason(t *testing.T) {
	t.Parallel()

	conditions := []metav1.Condition{{
		Type:    VRGConditionTypeDeprecatedWarnings,
		Status:  metav1.ConditionTrue,
		Reason:  "FutureDeprecation",
		Message: "some later warning",
	}}

	if setNonConsistencyGroupDeprecationCondition(&conditions, 1, false) {
		t.Fatal("clearing non-consistency-group usage should leave a different deprecation")
	}

	condition := meta.FindStatusCondition(conditions, VRGConditionTypeDeprecatedWarnings)
	if condition == nil || condition.Reason != "FutureDeprecation" {
		t.Fatalf("expected the other deprecation to remain, got %#v", condition)
	}
}

func TestVRGHasNonConsistencyGroupDeprecation(t *testing.T) {
	t.Parallel()

	if vrgHasNonConsistencyGroupDeprecation(nil) {
		t.Fatal("expected a missing VRG to have no deprecation")
	}

	vrg := &rmn.VolumeReplicationGroup{}
	if vrgHasNonConsistencyGroupDeprecation(vrg) {
		t.Fatal("expected no deprecation without the condition")
	}

	meta.SetStatusCondition(&vrg.Status.Conditions, metav1.Condition{
		Type:    VRGConditionTypeDeprecatedWarnings,
		Status:  metav1.ConditionTrue,
		Reason:  "FutureDeprecation",
		Message: "other",
	})

	if vrgHasNonConsistencyGroupDeprecation(vrg) {
		t.Fatal("expected a different deprecation reason to be ignored")
	}

	meta.SetStatusCondition(&vrg.Status.Conditions, metav1.Condition{
		Type:    VRGConditionTypeDeprecatedWarnings,
		Status:  metav1.ConditionTrue,
		Reason:  util.EventReasonNonConsistencyGroupDeprecated,
		Message: util.NonConsistencyGroupDeprecatedMessage,
	})

	if !vrgHasNonConsistencyGroupDeprecation(vrg) {
		t.Fatal("expected the non-consistency-group warning to be reported")
	}
}
