package workloads_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/helpers"
	"github.com/ramendr/ramen/e2e/workloads"
)

func TestNewDeployment(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("requires kubectl and access to GitHub")
	}

	repo := config.Repo{
		URL:    "https://github.com/RamenDR/ocm-ramen-samples.git",
		Branch: "7a778cca8005bd90da4188d7d3946c13d8ac359d", // Pin the manifests used by this test.
	}
	pvcSpec := config.PVCSpec{Name: "rbd", StorageClassName: "test-sc", AccessModes: "ReadWriteOnce"}

	actual, err := workloads.NewDeployment(t.Context(), repo, pvcSpec)
	if err != nil {
		t.Fatalf("error creating deployment: %v", err)
	}

	expected := &workloads.Deployment{
		Name:     "deploy-rbd",
		Branch:   repo.Branch,
		PVCSpec:  pvcSpec,
		PVCNames: []string{"busybox-pvc"},
	}

	if diff := helpers.UnifiedDiff(t, expected, actual); diff != "" {
		t.Fatalf("deployment mismatch:\n%s", diff)
	}
}

func TestNewDeploymentCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := workloads.NewDeployment(ctx, config.Repo{
		URL:    "https://example.com/repo.git",
		Branch: "main",
	}, config.PVCSpec{Name: "rbd"})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
}

func TestDeploymentKustomizePVCPatch(t *testing.T) {
	t.Parallel()

	deployment := workloads.Deployment{
		PVCSpec: config.PVCSpec{
			Name:             "rbd",
			StorageClassName: "test-sc",
			AccessModes:      "ReadWriteMany",
		},
		PVCNames: []string{"busybox-pvc"},
	}

	actualKustomize, err := deployment.Kustomize()
	if err != nil {
		t.Fatalf("error generating kustomize: %v", err)
	}

	expectedKustomize := `{"patches":[{` +
		`"patch":"[` +
		`{\"op\":\"add\",\"path\":\"/spec/storageClassName\",\"value\":\"test-sc\"},` +
		`{\"op\":\"add\",\"path\":\"/spec/accessModes\",\"value\":[\"ReadWriteMany\"]}` +
		`]",` +
		`"target":{"kind":"PersistentVolumeClaim","name":"busybox-pvc"}` +
		`}]}`

	if actualKustomize != expectedKustomize {
		t.Fatalf("expected:\n%s\ngot:\n%s", expectedKustomize, actualKustomize)
	}
}

func TestDeploymentKustomizeMultiplePVCs(t *testing.T) {
	t.Parallel()

	deployment := workloads.Deployment{
		PVCSpec: config.PVCSpec{
			Name:             "rbd",
			StorageClassName: "test-sc",
			AccessModes:      "ReadWriteMany",
		},
		PVCNames: []string{"pvc-1", "pvc-2"},
	}

	actualKustomize, err := deployment.Kustomize()
	if err != nil {
		t.Fatalf("error generating kustomize: %v", err)
	}

	expectedKustomize := `{"patches":[{` +
		`"patch":"[` +
		`{\"op\":\"add\",\"path\":\"/spec/storageClassName\",\"value\":\"test-sc\"},` +
		`{\"op\":\"add\",\"path\":\"/spec/accessModes\",\"value\":[\"ReadWriteMany\"]}` +
		`]",` +
		`"target":{"kind":"PersistentVolumeClaim","name":"pvc-1"}` +
		`},{` +
		`"patch":"[` +
		`{\"op\":\"add\",\"path\":\"/spec/storageClassName\",\"value\":\"test-sc\"},` +
		`{\"op\":\"add\",\"path\":\"/spec/accessModes\",\"value\":[\"ReadWriteMany\"]}` +
		`]",` +
		`"target":{"kind":"PersistentVolumeClaim","name":"pvc-2"}` +
		`}]}`

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
