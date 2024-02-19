// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Relationship type between two value sets.
type relationship int

const (
	overlap relationship = iota
	disjoint
	subset
	superset
	equal
)

func determineRelationship(a, b []string) relationship {
	setA := make(map[string]bool)
	setB := make(map[string]bool)

	for _, v := range a {
		setA[v] = true
	}
	for _, v := range b {
		setB[v] = true
	}

	// Check if A is a subset or equal to B
	isSubset := true
	for v := range setA {
		if !setB[v] {
			isSubset = false
			break
		}
	}

	// Check if B is a subset or equal to A
	isSuperset := true
	for v := range setB {
		if !setA[v] {
			isSuperset = false
			break
		}
	}

	if isSubset && isSuperset {
		return equal
	}
	if isSubset {
		return subset
	}
	if isSuperset {
		return superset
	}

	for v := range setA {
		if setB[v] {
			return overlap
		}
	}

	return disjoint
}

func isConflicting(op1, op2 v1.LabelSelectorOperator, rel relationship) bool {
	switch op1 {
	case v1.LabelSelectorOpIn:
		switch op2 {
		case v1.LabelSelectorOpIn:
			return rel == disjoint
		case v1.LabelSelectorOpNotIn:
			return rel == subset || rel == equal
		case v1.LabelSelectorOpDoesNotExist:
			return rel != disjoint
		}
	case v1.LabelSelectorOpNotIn:
		switch op2 {
		case v1.LabelSelectorOpIn:
			return rel == superset || rel == equal
		case v1.LabelSelectorOpDoesNotExist:
			return rel != disjoint
		}
	case v1.LabelSelectorOpExists:
		switch op2 {
		case v1.LabelSelectorOpNotIn, v1.LabelSelectorOpDoesNotExist:
			return true
		}
	case v1.LabelSelectorOpDoesNotExist:
		switch op2 {
		case v1.LabelSelectorOpIn, v1.LabelSelectorOpNotIn, v1.LabelSelectorOpExists:
			return true
		}
	}
	return false
}

func canMatchSameElements(selector1, selector2 *v1.LabelSelector) bool {
	// Convert matchLabels to LabelSelectorRequirements for unified processing
	allExprs1 := labelsToExpressions(selector1.MatchLabels)
	allExprs2 := labelsToExpressions(selector2.MatchLabels)

	// Consolidate all matchExpressions
	allExprs1 = append(allExprs1, selector1.MatchExpressions...)
	allExprs2 = append(allExprs2, selector2.MatchExpressions...)

	// Check if any expression contradicts the other selector
	return !selectorsWillNeverMatch(allExprs1, allExprs2)
}

func labelsToExpressions(matchLabels map[string]string) []v1.LabelSelectorRequirement {
	var exprs []v1.LabelSelectorRequirement
	for k, v := range matchLabels {
		exprs = append(exprs, v1.LabelSelectorRequirement{
			Key:      k,
			Operator: v1.LabelSelectorOpIn,
			Values:   []string{v},
		})
	}
	return exprs
}

func selectorsWillNeverMatch(exprs1, exprs2 []v1.LabelSelectorRequirement) bool {
	for _, e1 := range exprs1 {
		for _, e2 := range exprs2 {
			if e1.Key != e2.Key {
				continue
			}

			relationship := determineRelationship(e1.Values, e2.Values)
			if isConflicting(e1.Operator, e2.Operator, relationship) {
				return true
			}
		}
	}
	return false
}
