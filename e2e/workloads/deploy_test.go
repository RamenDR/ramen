package workloads_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/workloads"
)

func TestDeploymentKustomizePVCPatch(t *testing.T) {
	t.Parallel()

	storageClassName := "test-sc"
	accessMode := "ReadWriteMany"

	deployment := workloads.Deployment{
		PVCSpec: config.PVCSpec{
			Name:             "rbd",
			StorageClassName: storageClassName,
			AccessModes:      accessMode,
		},
	}

	actualKustomize, err := deployment.Kustomize()
	if err != nil {
		t.Fatalf("error generating kustomize: %v", err)
	}

	expectedKustomize := `{"patches":[` +
		`{"patch":"[{\"op\":\"replace\",\"path\":\"/spec/storageClassName\",\"value\":\"test-sc\"},
		            {\"op\":\"add\",\"path\":\"/spec/accessModes\",\"value\":[\"ReadWriteMany\"]}]",
					"target":{"kind":"PersistentVolumeClaim","name":"busybox-pvc"}}` +
		`]}`

	if actualKustomize != expectedKustomize {
		t.Fatalf("expected:\n%s\ngot:\n%s", expectedKustomize, actualKustomize)
	}
}

func TestDeploymentKustomizeEmptyPVCSpec(t *testing.T) {
	t.Parallel()

	deployment := workloads.Deployment{}

	kustomize, err := deployment.Kustomize()
	if err == nil {
		t.Fatalf("expected error when pvcSpec is empty, got %q", kustomize)
	}
}

func TestDeploymentKustomizeRejectsEmptyStorageClass(t *testing.T) {
	t.Parallel()

	deployment := workloads.Deployment{
		PVCSpec: config.PVCSpec{
			Name:        "rbd",
			AccessModes: "ReadWriteMany",
		},
	}

	kustomize, err := deployment.Kustomize()
	if err == nil {
		t.Fatalf("expected error when storageClassName is empty, got %q", kustomize)
	}
}

func TestDeploymentKustomizeRejectsEmptyAccessModes(t *testing.T) {
	t.Parallel()

	deployment := workloads.Deployment{
		PVCSpec: config.PVCSpec{
			Name:             "rbd",
			StorageClassName: "test-sc",
		},
	}

	kustomize, err := deployment.Kustomize()
	if err == nil {
		t.Fatalf("expected error when accessModes is empty, got %q", kustomize)
	}
}
