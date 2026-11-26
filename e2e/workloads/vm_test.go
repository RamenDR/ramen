package workloads_test

import (
	"encoding/json"
	"testing"

	"github.com/ramendr/ramen/e2e/argocd"
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
		Name:     "vm-pvc-rbd",
		Branch:   repo.Branch,
		PVCSpec:  pvcSpec,
		PVCNames: []string{"root-disk"},
	}

	if diff := helpers.UnifiedDiff(t, expected, vm); diff != "" {
		t.Fatalf("vm mismatch:\n%s", diff)
	}
}

func TestVMKustomizePatches(t *testing.T) {
	t.Parallel()

	vm := workloads.VM{
		PVCSpec: config.PVCSpec{
			Name:             "rbd",
			StorageClassName: "rook-ceph-block",
			AccessModes:      "ReadWriteOnce",
		},
		PVCNames: []string{"os-disk", "data-disk"},
	}

	actualKustomize, err := vm.Kustomize()
	if err != nil {
		t.Fatalf("error generating kustomize: %v", err)
	}

	var actual argocd.ApplicationSourceKustomize
	if err := json.Unmarshal([]byte(actualKustomize), &actual); err != nil {
		t.Fatalf("decode Kustomize output: %v", err)
	}

	expected := argocd.ApplicationSourceKustomize{
		Patches: argocd.KustomizePatches{
			{
				Target: &argocd.KustomizeSelector{
					KustomizeResId: argocd.KustomizeResId{
						KustomizeGvk: argocd.KustomizeGvk{Kind: "VirtualMachine"},
						Name:         "vm",
					},
				},
				Patch: `[{"op":"add","path":"/spec/template/spec/domain/devices/interfaces/0/bridge","value":{}}]`,
			},
			{
				Target: &argocd.KustomizeSelector{
					KustomizeResId: argocd.KustomizeResId{
						KustomizeGvk: argocd.KustomizeGvk{Kind: "PersistentVolumeClaim"},
						Name:         "os-disk",
					},
				},
				Patch: `[{"op":"add","path":"/spec/storageClassName","value":"rook-ceph-block"},` +
					`{"op":"add","path":"/spec/accessModes","value":["ReadWriteOnce"]}]`,
			},
			{
				Target: &argocd.KustomizeSelector{
					KustomizeResId: argocd.KustomizeResId{
						KustomizeGvk: argocd.KustomizeGvk{Kind: "PersistentVolumeClaim"},
						Name:         "data-disk",
					},
				},
				Patch: `[{"op":"add","path":"/spec/storageClassName","value":"rook-ceph-block"},` +
					`{"op":"add","path":"/spec/accessModes","value":["ReadWriteOnce"]}]`,
			},
		},
	}

	if diff := helpers.UnifiedDiff(t, expected, actual); diff != "" {
		t.Fatalf("Kustomize output differs:\n%s", diff)
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
