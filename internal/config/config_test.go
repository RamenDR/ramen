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

func TestMergeUserFieldsNotInDefault(t *testing.T) {
	system := []byte(`
volSync:
  disabled: false
`)
	user := []byte(`
maxConcurrentReconciles: 77
kubeObjectProtection:
  veleroNamespaceName: my-velero
`)

	merged, err := config.Merge(system, user)
	if err != nil {
		t.Fatal(err)
	}

	if merged.MaxConcurrentReconciles != 77 {
		t.Errorf("expected maxConcurrentReconciles=77 from user, got %d", merged.MaxConcurrentReconciles)
	}

	if merged.KubeObjectProtection.VeleroNamespaceName != "my-velero" {
		t.Errorf("expected kubeObjectProtection.veleroNamespaceName from user, got %q",
			merged.KubeObjectProtection.VeleroNamespaceName)
	}

	if merged.VolSync.Disabled {
		t.Error("expected volSync.disabled=false from system")
	}
}

func TestMergeOverrideSameFieldsScalarsAndNested(t *testing.T) {
	system := []byte(`
maxConcurrentReconciles: 10
volSync:
  disabled: false
  destinationCopyMethod: Snapshot
`)
	user := []byte(`
maxConcurrentReconciles: 25
volSync:
  disabled: true
  destinationCopyMethod: Direct
`)

	merged, err := config.Merge(system, user)
	if err != nil {
		t.Fatal(err)
	}

	if merged.MaxConcurrentReconciles != 25 {
		t.Errorf("expected maxConcurrentReconciles=25 from user, got %d", merged.MaxConcurrentReconciles)
	}

	if merged.VolSync.DestinationCopyMethod != "Direct" {
		t.Errorf("expected volSync.destinationCopyMethod=Direct from user, got %q",
			merged.VolSync.DestinationCopyMethod)
	}

	if !merged.VolSync.Disabled {
		t.Error("expected volSync.disabled=true from user")
	}
}

func TestMergePreserveDefaultNewFieldsNotInUserYAML(t *testing.T) {
	system := []byte(`
volSync:
  disabled: false
health:
  readinessEndpointName: readyz-from-system
volumeUnprotectionEnabled: true
`)
	user := []byte(`
volSync:
  disabled: true
`)

	merged, err := config.Merge(system, user)
	if err != nil {
		t.Fatal(err)
	}

	if merged.Health.ReadinessEndpointName != "readyz-from-system" {
		t.Errorf("expected health.readinessEndpointName from system, got %q", merged.Health.ReadinessEndpointName)
	}

	if !merged.VolumeUnprotectionEnabled {
		t.Error("expected volumeUnprotectionEnabled=true from system (user YAML omitted these keys)")
	}

	if !merged.VolSync.Disabled {
		t.Error("expected volSync.disabled=true from user override")
	}
}
