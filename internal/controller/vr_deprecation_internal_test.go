// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"slices"
	"testing"

	"github.com/go-logr/logr"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

func TestNonConsistencyGroupDeprecationWarning(t *testing.T) {
	warnings := []rmn.Warning{{
		Name:    "FutureDeprecation",
		Message: "some later warning",
	}}

	setWarning(
		logr.Discard(),
		&warnings,
		rmn.WarningNonConsistencyGroupDeprecated,
		util.VolumeReplicationDeprecatedMessage,
	)

	expected := []rmn.Warning{
		{Name: "FutureDeprecation", Message: "some later warning"},
		{
			Name:    rmn.WarningNonConsistencyGroupDeprecated,
			Message: util.VolumeReplicationDeprecatedMessage,
		},
	}
	if !slices.Equal(warnings, expected) {
		t.Fatalf("warnings = %#v, want %#v", warnings, expected)
	}

	removeWarning(logr.Discard(), &warnings, rmn.WarningNonConsistencyGroupDeprecated)

	expected = []rmn.Warning{{
		Name:    "FutureDeprecation",
		Message: "some later warning",
	}}
	if !slices.Equal(warnings, expected) {
		t.Fatalf("warnings = %#v, want %#v", warnings, expected)
	}

	removeWarning(logr.Discard(), &warnings, rmn.WarningNonConsistencyGroupDeprecated)

	if !slices.Equal(warnings, expected) {
		t.Fatalf("warnings = %#v, want %#v", warnings, expected)
	}
}

func TestRemoveLastDeprecationWarning(t *testing.T) {
	var warnings []rmn.Warning

	setWarning(
		logr.Discard(),
		&warnings,
		rmn.WarningNonConsistencyGroupDeprecated,
		util.VolumeReplicationDeprecatedMessage,
	)

	removeWarning(logr.Discard(), &warnings, rmn.WarningNonConsistencyGroupDeprecated)

	if warnings != nil {
		t.Fatalf("warnings = %#v, want nil", warnings)
	}
}
