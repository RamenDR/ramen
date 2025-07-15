// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildKustomization(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kustomize-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	kustomizationContent := `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - deployment.yaml
`

	deploymentContent := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: test
  template:
    metadata:
      labels:
        app: test
    spec:
      containers:
      - name: test-container
        image: nginx:latest
`

	if err := os.WriteFile(filepath.Join(tempDir, "kustomization.yaml"), []byte(kustomizationContent), 0644); err != nil {
		t.Fatalf("Failed to write kustomization.yaml: %v", err)
	}

	if err := os.WriteFile(filepath.Join(tempDir, "deployment.yaml"), []byte(deploymentContent), 0644); err != nil {
		t.Fatalf("Failed to write deployment.yaml: %v", err)
	}

	result, err := buildKustomization(tempDir)
	if err != nil {
		t.Fatalf("buildKustomization failed: %v", err)
	}

	resultStr := string(result)
	if !containsAll(resultStr, []string{"apiVersion: apps/v1", "kind: Deployment", "name: test-deployment"}) {
		t.Errorf("buildKustomization result does not contain expected content. Got:\n%s", resultStr)
	}
}

func TestParseYAMLResources(t *testing.T) {
	yamlContent := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment
  namespace: default
spec:
  replicas: 1
---
apiVersion: v1
kind: Service
metadata:
  name: test-service
  namespace: default
spec:
  selector:
    app: test
  ports:
  - port: 80
    targetPort: 8080
`

	resources, err := parseYAMLResources([]byte(yamlContent))
	if err != nil {
		t.Fatalf("parseYAMLResources failed: %v", err)
	}

	if len(resources) != 2 {
		t.Fatalf("Expected 2 resources, got %d", len(resources))
	}

	if resources[0].GetKind() != "Deployment" {
		t.Errorf("Expected first resource to be Deployment, got %s", resources[0].GetKind())
	}

	if resources[0].GetName() != "test-deployment" {
		t.Errorf("Expected first resource name to be test-deployment, got %s", resources[0].GetName())
	}

	if resources[1].GetKind() != "Service" {
		t.Errorf("Expected second resource to be Service, got %s", resources[1].GetKind())
	}

	if resources[1].GetName() != "test-service" {
		t.Errorf("Expected second resource name to be test-service, got %s", resources[1].GetName())
	}
}

func containsAll(str string, substrings []string) bool {
	for _, substr := range substrings {
		if !contains(str, substr) {
			return false
		}
	}
	return true
}

func contains(str, substr string) bool {
	return len(str) >= len(substr) && indexOfSubstring(str, substr) >= 0
}

func indexOfSubstring(str, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	for i := 0; i <= len(str)-len(substr); i++ {
		if str[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
