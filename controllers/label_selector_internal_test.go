// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"testing"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//nolint:funlen
func TestCanMatchSameElements(t *testing.T) {
	tests := []struct {
		name        string
		selectorA   *v1.LabelSelector
		selectorB   *v1.LabelSelector
		shouldMatch bool
	}{
		{
			name: "Test with two keys where one can match",
			selectorA: &v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "tier", Operator: v1.LabelSelectorOpIn, Values: []string{"frontend"}},
					{Key: "env", Operator: v1.LabelSelectorOpIn, Values: []string{"dev", "staging"}},
				},
			},
			selectorB: &v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "tier", Operator: v1.LabelSelectorOpIn, Values: []string{"frontend", "backend"}},
					{Key: "env", Operator: v1.LabelSelectorOpNotIn, Values: []string{"prod"}},
				},
			},
			shouldMatch: true,
		},
		{
			name: "Test with one k=v and one k exists",
			selectorA: &v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "tier", Operator: v1.LabelSelectorOpIn, Values: []string{"frontend"}},
				},
			},
			selectorB: &v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "tier", Operator: v1.LabelSelectorOpExists},
				},
			},
			shouldMatch: true,
		},
		{
			name: "Test with k={v1} and k={v2}",
			selectorA: &v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "tier", Operator: v1.LabelSelectorOpIn, Values: []string{"cache"}},
				},
			},
			selectorB: &v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "tier", Operator: v1.LabelSelectorOpIn, Values: []string{"backup"}},
				},
			},
			shouldMatch: false,
		},
		{
			name: "Test with k1={v1},k2!={v3} and k={v2},k2!={v3}",
			selectorA: &v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "tier", Operator: v1.LabelSelectorOpIn, Values: []string{"cache"}},
					{Key: "env", Operator: v1.LabelSelectorOpNotIn, Values: []string{"dev"}},
				},
			},
			selectorB: &v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "tier", Operator: v1.LabelSelectorOpIn, Values: []string{"backup"}},
					{Key: "env", Operator: v1.LabelSelectorOpNotIn, Values: []string{"dev"}},
				},
			},
			shouldMatch: false,
		},
		{
			name: "Test with cache and not cache",
			selectorA: &v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "tier", Operator: v1.LabelSelectorOpIn, Values: []string{"cache"}},
				},
			},
			selectorB: &v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "tier", Operator: v1.LabelSelectorOpNotIn, Values: []string{"cache"}},
				},
			},
			shouldMatch: false,
		},
		{
			name:      "Test with empty selector",
			selectorA: &v1.LabelSelector{},
			selectorB: &v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "tier", Operator: v1.LabelSelectorOpIn, Values: []string{"backup"}},
				},
			},
			shouldMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if match := canMatchSameElements(tt.selectorA, tt.selectorB); match != tt.shouldMatch {
				if tt.shouldMatch {
					t.Fatalf("Expected selectors to potentially match the same elements, but they were determined not to.")
				} else {
					t.Fatalf("Expected selectors to not match the same elements, but they were determined to.")
				}
			}
		})
	}
}
