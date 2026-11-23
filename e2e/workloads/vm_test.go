package workloads_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/helpers"
	"github.com/ramendr/ramen/e2e/workloads"
)

func TestNewVM(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("requires kubectl and access to GitHub")
	}

	repo := config.Repo{
		URL:    "https://github.com/RamenDR/ocm-ramen-samples.git",
		Branch: "7a778cca8005bd90da4188d7d3946c13d8ac359d", // Pin the manifests used by this test.
	}
	pvcSpec := config.PVCSpec{Name: "rbd", StorageClassName: "test-sc", AccessModes: "ReadWriteOnce"}

	actual, err := workloads.NewVM(t.Context(), repo, pvcSpec)
	if err != nil {
		t.Fatalf("error creating VM: %v", err)
	}

	expected := &workloads.VM{
		Name:     "vm-pvc-rbd",
		Branch:   repo.Branch,
		PVCSpec:  pvcSpec,
		PVCNames: []string{"root-disk"},
	}

	if diff := helpers.UnifiedDiff(t, expected, actual); diff != "" {
		t.Fatalf("VM mismatch:\n%s", diff)
	}
}

func TestVMKustomizePatches(t *testing.T) {
	t.Parallel()

	storageClassName := "test-sc"
	accessMode := "ReadWriteMany"

	vm := workloads.VM{
		PVCSpec: config.PVCSpec{
			Name:             "rbd",
			StorageClassName: storageClassName,
			AccessModes:      accessMode,
		},
		PVCNames: []string{"os-disk", "data-disk"},
	}

	actualKustomize, err := vm.Kustomize()
	if err != nil {
		t.Fatalf("error generating kustomize: %v", err)
	}

	expectedKustomize := `{"patches":[{` +
		`"patch":"[` +
		`{\"op\":\"add\",\"path\":\"/spec/template/spec/domain/devices/interfaces/0/bridge\",\"value\":{}}` +
		`]",` +
		`"target":{"kind":"VirtualMachine","name":"vm"}` +
		`},{` +
		`"patch":"[` +
		`{\"op\":\"add\",\"path\":\"/spec/storageClassName\",\"value\":\"test-sc\"},` +
		`{\"op\":\"add\",\"path\":\"/spec/accessModes\",\"value\":[\"ReadWriteMany\"]}` +
		`]",` +
		`"target":{"kind":"PersistentVolumeClaim","name":"os-disk"}` +
		`},{` +
		`"patch":"[` +
		`{\"op\":\"add\",\"path\":\"/spec/storageClassName\",\"value\":\"test-sc\"},` +
		`{\"op\":\"add\",\"path\":\"/spec/accessModes\",\"value\":[\"ReadWriteMany\"]}` +
		`]",` +
		`"target":{"kind":"PersistentVolumeClaim","name":"data-disk"}` +
		`}]}`

	if actualKustomize != expectedKustomize {
		t.Fatalf("expected:\n%s\ngot:\n%s", expectedKustomize, actualKustomize)
	}
}

func TestVMKustomizeRejectsEmptyStorageClass(t *testing.T) {
	t.Parallel()

	vm := workloads.VM{
		PVCSpec: config.PVCSpec{
			Name:        "rbd",
			AccessModes: "ReadWriteMany",
		},
	}

	kustomize, err := vm.Kustomize()
	if err == nil {
		t.Fatalf("expected error when storageClassName is empty, got %q", kustomize)
	}
}

func TestVMKustomizeRejectsEmptyAccessModes(t *testing.T) {
	t.Parallel()

	vm := workloads.VM{
		PVCSpec: config.PVCSpec{
			Name:             "rbd",
			StorageClassName: "test-sc",
		},
	}

	kustomize, err := vm.Kustomize()
	if err == nil {
		t.Fatalf("expected error when accessModes is empty, got %q", kustomize)
	}
}
