package workloads_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/helpers"
	"github.com/ramendr/ramen/e2e/workloads"
)

func TestNewDeploymentCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	repo := config.Repo{URL: "https://example.com/repo.git", Branch: "main"}
	pvcSpec := config.PVCSpec{Name: "rbd"}

	_, err := workloads.NewDeployment(ctx, repo, pvcSpec)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
}

func TestNewDeploymentCreate(t *testing.T) {
	t.Parallel()

	// Pin the sample revision so upstream changes cannot alter the PVC names this test expects.
	repo := config.Repo{
		URL:    "https://github.com/RamenDR/ocm-ramen-samples.git",
		Branch: "7a778cca8005bd90da4188d7d3946c13d8ac359d",
	}
	pvcSpec := config.PVCSpec{
		Name:             "rbd",
		StorageClassName: "rook-ceph-block",
		AccessModes:      "ReadWriteOnce",
	}

	deployment, err := workloads.NewDeployment(t.Context(), repo, pvcSpec)
	if err != nil {
		t.Fatalf("NewDeployment: %v", err)
	}

	expected := &workloads.Deployment{
		Name:     "deploy-rbd",
		Branch:   repo.Branch,
		PVCSpec:  pvcSpec,
		PVCNames: []string{"busybox-pvc"},
	}

	if diff := helpers.UnifiedDiff(t, expected, deployment); diff != "" {
		t.Fatalf("deployment mismatch:\n%s", diff)
	}
}

func TestDeploymentKustomizePVCPatch(t *testing.T) {
	t.Parallel()

	deployment := workloads.Deployment{
		PVCSpec: config.PVCSpec{
			Name:             "rbd",
			StorageClassName: "rook-ceph-block",
			AccessModes:      "ReadWriteOnce",
		},
		PVCNames: []string{"busybox-pvc"},
	}

	actualKustomize, err := deployment.Kustomize()
	if err != nil {
		t.Fatalf("error generating kustomize: %v", err)
	}

	expectedKustomize := `{"patches":[{` +
		`"patch":"[` +
		`{\"op\":\"add\",\"path\":\"/spec/storageClassName\",\"value\":\"rook-ceph-block\"},` +
		`{\"op\":\"add\",\"path\":\"/spec/accessModes\",\"value\":[\"ReadWriteOnce\"]}` +
		`]",` +
		`"target":{"kind":"PersistentVolumeClaim","name":"busybox-pvc"}` +
		`}]}`

	if diff := helpers.UnifiedDiff(t, expectedKustomize, actualKustomize); diff != "" {
		t.Fatalf("kustomize mismatch:\n%s", diff)
	}
}

func TestDeploymentKustomizeMultiplePVCs(t *testing.T) {
	t.Parallel()

	deployment := workloads.Deployment{
		PVCSpec: config.PVCSpec{
			Name:             "rbd",
			StorageClassName: "rook-ceph-block",
			AccessModes:      "ReadWriteOnce",
		},
		PVCNames: []string{"pvc-1", "pvc-2"},
	}

	actualKustomize, err := deployment.Kustomize()
	if err != nil {
		t.Fatalf("error generating kustomize: %v", err)
	}

	expectedKustomize := `{"patches":[{` +
		`"patch":"[` +
		`{\"op\":\"add\",\"path\":\"/spec/storageClassName\",\"value\":\"rook-ceph-block\"},` +
		`{\"op\":\"add\",\"path\":\"/spec/accessModes\",\"value\":[\"ReadWriteOnce\"]}` +
		`]",` +
		`"target":{"kind":"PersistentVolumeClaim","name":"pvc-1"}` +
		`},{` +
		`"patch":"[` +
		`{\"op\":\"add\",\"path\":\"/spec/storageClassName\",\"value\":\"rook-ceph-block\"},` +
		`{\"op\":\"add\",\"path\":\"/spec/accessModes\",\"value\":[\"ReadWriteOnce\"]}` +
		`]",` +
		`"target":{"kind":"PersistentVolumeClaim","name":"pvc-2"}` +
		`}]}`

	if diff := helpers.UnifiedDiff(t, expectedKustomize, actualKustomize); diff != "" {
		t.Fatalf("kustomize mismatch:\n%s", diff)
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
			AccessModes: "ReadWriteOnce",
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
			StorageClassName: "rook-ceph-block",
		},
	}

	kustomize, err := deployment.Kustomize()
	if err == nil {
		t.Fatalf("expected error when accessModes is empty, got %q", kustomize)
	}
}
