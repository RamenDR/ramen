package workloads_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/workloads"
)

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
