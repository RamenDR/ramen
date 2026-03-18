// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"testing"

	"github.com/ramendr/ramen/internal/config"
)

func TestMergeBooleanOverride(t *testing.T) {
	system := []byte(`
volSync:
  disabled: false
`)
	user := []byte(`
volSync:
  disabled: true
`)

	merged, err := config.Merge(system, user)
	if err != nil {
		t.Fatal(err)
	}

	if !merged.VolSync.Disabled {
		t.Error("expected Disabled=true from user override")
	}
}

func TestMergeBooleanPreserveDefault(t *testing.T) {
	system := []byte(`
volSync:
  disabled: false
maxConcurrentReconciles: 10
`)
	user := []byte(``)

	merged, err := config.Merge(system, user)
	if err != nil {
		t.Fatal(err)
	}

	if merged.VolSync.Disabled {
		t.Error("expected Disabled=false preserved from system")
	}

	if merged.MaxConcurrentReconciles != 10 {
		t.Errorf("expected MaxConcurrentReconciles=10, got %d", merged.MaxConcurrentReconciles)
	}
}

func TestMergeBooleanExplicitFalseOverridesTrue(t *testing.T) {
	system := []byte(`
volSync:
  disabled: true
`)
	user := []byte(`
volSync:
  disabled: false
`)

	merged, err := config.Merge(system, user)
	if err != nil {
		t.Fatal(err)
	}

	if merged.VolSync.Disabled {
		t.Error("expected Disabled=false from explicit user override")
	}
}
