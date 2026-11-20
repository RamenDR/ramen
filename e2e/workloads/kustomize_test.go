// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads //nolint:testpackage // tests unexported pvcNames

import (
	"slices"
	"testing"
)

func TestPVCNamesSinglePVC(t *testing.T) {
	t.Parallel()

	kustomization := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: busybox
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: busybox-pvc
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
`

	names, err := pvcNames(kustomization)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"busybox-pvc"}
	if !slices.Equal(names, expected) {
		t.Fatalf("expected %v, got %v", expected, names)
	}
}

func TestPVCNamesMultiplePVCs(t *testing.T) {
	t.Parallel()

	kustomization := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: data
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: pvc-1
---
apiVersion: v1
kind: Service
metadata:
  name: svc
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: pvc-2
`

	names, err := pvcNames(kustomization)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"pvc-1", "pvc-2"}
	if !slices.Equal(names, expected) {
		t.Fatalf("expected %v, got %v", expected, names)
	}
}

func TestPVCNamesNoPVC(t *testing.T) {
	t.Parallel()

	kustomization := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: busybox
`

	_, err := pvcNames(kustomization)
	if err == nil {
		t.Fatal("expected error when no PVCs are present")
	}
}

func TestPVCNamesInvalidYAML(t *testing.T) {
	t.Parallel()

	_, err := pvcNames("apiVersion: [")
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}
