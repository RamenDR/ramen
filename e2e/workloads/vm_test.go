package workloads_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/helpers"
	"github.com/ramendr/ramen/e2e/workloads"
)

func TestNewVM(t *testing.T) {
	t.Parallel()

	// Pin the VM sample revision so the fixture stays the same when upstream main changes.
	repo := config.Repo{
		URL:    "https://github.com/RamenDR/ocm-ramen-samples.git",
		Branch: "7a778cca8005bd90da4188d7d3946c13d8ac359d",
	}
	pvcSpec := config.PVCSpec{
		Name:             "rbd",
		StorageClassName: "rook-ceph-block",
		AccessModes:      "ReadWriteOnce",
	}

	vm, err := workloads.NewVM(t.Context(), repo, pvcSpec)
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}

	expected := &workloads.VM{
		Name:    "vm-pvc-rbd",
		Branch:  repo.Branch,
		PVCSpec: pvcSpec,
	}

	if diff := helpers.UnifiedDiff(t, expected, vm); diff != "" {
		t.Fatalf("vm mismatch:\n%s", diff)
	}
}

func TestVMKustomizePatches(t *testing.T) {
	t.Parallel()

	storageClassName := "rook-ceph-block"
	accessMode := "ReadWriteOnce"

	vm := workloads.VM{
		PVCSpec: config.PVCSpec{
			Name:             "rbd",
			StorageClassName: storageClassName,
			AccessModes:      accessMode,
		},
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
		`{\"op\":\"add\",\"path\":\"/spec/storageClassName\",\"value\":\"rook-ceph-block\"},` +
		`{\"op\":\"add\",\"path\":\"/spec/accessModes\",\"value\":[\"ReadWriteOnce\"]}` +
		`]",` +
		`"target":{"kind":"PersistentVolumeClaim","name":"root-disk"}` +
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
			AccessModes: "ReadWriteOnce",
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
			StorageClassName: "rook-ceph-block",
		},
	}

	kustomize, err := vm.Kustomize()
	if err == nil {
		t.Fatalf("expected error when accessModes is empty, got %q", kustomize)
	}
}
