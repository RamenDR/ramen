// Disabling testpackage linter to test unexported splitDocuments.
//
//nolint:testpackage
package workloads

import (
	"slices"
	"testing"

	"github.com/ramendr/ramen/e2e/config"
)

func TestDeploymentKustomizePVCPatch(t *testing.T) {
	t.Parallel()

	deployment := Deployment{
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
		`{\"op\":\"replace\",\"path\":\"/spec/storageClassName\",\"value\":\"test-sc\"},` +
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

	deployment := Deployment{
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
		`{\"op\":\"replace\",\"path\":\"/spec/storageClassName\",\"value\":\"test-sc\"},` +
		`{\"op\":\"add\",\"path\":\"/spec/accessModes\",\"value\":[\"ReadWriteMany\"]}` +
		`]",` +
		`"target":{"kind":"PersistentVolumeClaim","name":"pvc-1"}` +
		`},{` +
		`"patch":"[` +
		`{\"op\":\"replace\",\"path\":\"/spec/storageClassName\",\"value\":\"test-sc\"},` +
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

	deployment := Deployment{}

	kustomize, err := deployment.Kustomize()
	if err == nil {
		t.Fatalf("expected error when pvcSpec is empty, got %q", kustomize)
	}
}

func TestDeploymentKustomizeRejectsEmptyStorageClass(t *testing.T) {
	t.Parallel()

	deployment := Deployment{
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

	deployment := Deployment{
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

func TestSplitDocuments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "separator at start",
			input: "---\ndoc 1\n",
			want:  []string{"doc 1\n"},
		},
		{
			name:  "separator between documents",
			input: "doc 1\n---\ndoc 2\n",
			want:  []string{"doc 1", "doc 2\n"},
		},
		{
			name:  "separator at end",
			input: "doc 1\n---",
			want:  []string{"doc 1"},
		},
		{
			name:  "separator at start, between documents, and at end",
			input: "---\ndoc 1\n---\ndoc 2\n---",
			want:  []string{"doc 1", "doc 2"},
		},
		{
			name:  "separator inside a line",
			input: "doc 1\nfoo---\nmore\n---\ndoc 2\n",
			want:  []string{"doc 1\nfoo---\nmore", "doc 2\n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := splitDocuments(tt.input)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("expected %#v, got %#v", tt.want, got)
			}
		})
	}
}
