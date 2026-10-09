// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestApplyKustomizationWithFakeClient(t *testing.T) {
	fakeClient := createFakeClient()

	tempDir := createTestKustomization(t, map[string]string{
		"deployment.yaml": getDeploymentYAML("test-deployment", "default"),
		"service.yaml":    getServiceYAML("test-service", "default"),
	})
	defer os.RemoveAll(tempDir)

	ctx := &mockTestContext{
		ctx: context.Background(),
	}

	resMap, err := buildKustomization(tempDir)
	if err != nil {
		t.Fatalf("buildKustomization failed: %v", err)
	}

	err = applyResources(ctx, fakeClient, resMap, "default")
	if err != nil {
		t.Fatalf("applyYAML failed: %v", err)
	}

	deployment := &appsv1.Deployment{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{
		Name:      "test-deployment",
		Namespace: "default",
	}, deployment)
	if err != nil {
		t.Fatalf("Deployment not found after apply: %v", err)
	}
	if deployment.Name != "test-deployment" {
		t.Errorf("Expected deployment name 'test-deployment', got %s", deployment.Name)
	}

	service := &corev1.Service{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{
		Name:      "test-service",
		Namespace: "default",
	}, service)
	if err != nil {
		t.Fatalf("Service not found after apply: %v", err)
	}
	if service.Name != "test-service" {
		t.Errorf("Expected service name 'test-service', got %s", service.Name)
	}

	err = deleteResources(ctx, fakeClient, resMap, "default")
	if err != nil {
		t.Fatalf("deleteYAML failed: %v", err)
	}

	err = fakeClient.Get(context.Background(), client.ObjectKey{
		Name:      "test-deployment",
		Namespace: "default",
	}, deployment)
	if err == nil {
		t.Errorf("Deployment should be deleted, but still exists")
	}

	err = fakeClient.Get(context.Background(), client.ObjectKey{
		Name:      "test-service",
		Namespace: "default",
	}, service)
	if err == nil {
		t.Errorf("Service should be deleted, but still exists")
	}
}

func TestBuildKustomization(t *testing.T) {
	tempDir := createTestKustomization(t, map[string]string{
		"deployment.yaml": getDeploymentYAML("test-deployment", "default"),
	})
	defer os.RemoveAll(tempDir)

	resMap, err := buildKustomization(tempDir)
	if err != nil {
		t.Fatalf("buildKustomization failed: %v", err)
	}

	resources := resMap.Resources()
	if len(resources) == 0 {
		t.Fatalf("Expected at least 1 resource, got 0")
	}

	resource := resources[0]
	objectMap, err := resource.Map()
	if err != nil {
		t.Fatalf("Failed to get resource map: %v", err)
	}

	obj := &unstructured.Unstructured{}
	obj.SetUnstructuredContent(objectMap)

	if obj.GetKind() != "Deployment" {
		t.Errorf("Expected resource kind to be Deployment, got %s", obj.GetKind())
	}

	if obj.GetName() != "test-deployment" {
		t.Errorf("Expected resource name to be test-deployment, got %s", obj.GetName())
	}

	if obj.GetAPIVersion() != "apps/v1" {
		t.Errorf("Expected apiVersion to be apps/v1, got %s", obj.GetAPIVersion())
	}
}

func TestDeleteResources(t *testing.T) {
	tempDir := createTestKustomization(t, map[string]string{
		"deployment.yaml": getDeploymentYAML("test-deployment", "default"),
		"service.yaml":    getServiceYAML("test-service", "default"),
	})
	defer os.RemoveAll(tempDir)

	ctx := &mockTestContext{
		ctx: context.Background(),
	}

	resMap, err := buildKustomization(tempDir)
	if err != nil {
		t.Fatalf("buildKustomization failed: %v", err)
	}

	fakeClient := createFakeClient()

	err = applyResources(ctx, fakeClient, resMap, "default")
	if err != nil {
		t.Fatalf("applyResources failed: %v", err)
	}

	deployment := &appsv1.Deployment{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{
		Name:      "test-deployment",
		Namespace: "default",
	}, deployment)
	if err != nil {
		t.Fatalf("Deployment not found after apply: %v", err)
	}

	service := &corev1.Service{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{
		Name:      "test-service",
		Namespace: "default",
	}, service)
	if err != nil {
		t.Fatalf("Service not found after apply: %v", err)
	}

	err = deleteResources(ctx, fakeClient, resMap, "default")
	if err != nil {
		t.Fatalf("deleteResources failed: %v", err)
	}

	err = fakeClient.Get(context.Background(), client.ObjectKey{
		Name:      "test-deployment",
		Namespace: "default",
	}, deployment)
	if err == nil {
		t.Errorf("Deployment should be deleted, but still exists")
	}

	err = fakeClient.Get(context.Background(), client.ObjectKey{
		Name:      "test-service",
		Namespace: "default",
	}, service)
	if err == nil {
		t.Errorf("Service should be deleted, but still exists")
	}
}

func TestDeleteResourcesNonExistent(t *testing.T) {
	tempDir := createTestKustomization(t, map[string]string{
		"deployment.yaml": getDeploymentYAML("nonexistent-deployment", "default"),
	})
	defer os.RemoveAll(tempDir)

	ctx := &mockTestContext{
		ctx: context.Background(),
	}

	resMap, err := buildKustomization(tempDir)
	if err != nil {
		t.Fatalf("buildKustomization failed: %v", err)
	}

	fakeClient := createFakeClient()

	err = deleteResources(ctx, fakeClient, resMap, "default")
	if err != nil {
		t.Fatalf("deleteResources should not fail when deleting non-existent resources: %v", err)
	}
}

func getKustomizationYAML(resources []string) string {
	resourceList := ""
	for _, resource := range resources {
		resourceList += "  - " + resource + "\n"
	}
	return `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
` + resourceList
}

func getDeploymentYAML(name, namespace string) string {
	return `apiVersion: apps/v1
kind: Deployment
metadata:
  name: ` + name + `
  namespace: ` + namespace + `
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
}

func getServiceYAML(name, namespace string) string {
	return `apiVersion: v1
kind: Service
metadata:
  name: ` + name + `
  namespace: ` + namespace + `
spec:
  selector:
    app: test
  ports:
  - port: 80
    targetPort: 8080
`
}

func createTestKustomization(t *testing.T, resources map[string]string) string {
	tempDir, err := os.MkdirTemp("", "kustomize-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	var resourceNames []string
	for filename, content := range resources {
		if filename != "kustomization.yaml" {
			resourceNames = append(resourceNames, filename)
		}
		if err := os.WriteFile(filepath.Join(tempDir, filename), []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write %s: %v", filename, err)
		}
	}

	if _, exists := resources["kustomization.yaml"]; !exists {
		kustomizationContent := getKustomizationYAML(resourceNames)
		if err := os.WriteFile(filepath.Join(tempDir, "kustomization.yaml"), []byte(kustomizationContent), 0644); err != nil {
			t.Fatalf("Failed to write kustomization.yaml: %v", err)
		}
	}

	return tempDir
}

func createFakeClient(objects ...client.Object) client.Client {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = corev1.AddToScheme(s)

	return fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()
}

type mockTestContext struct {
	ctx context.Context
}

func (m *mockTestContext) Context() context.Context {
	return m.ctx
}

func (m *mockTestContext) Logger() *zap.SugaredLogger {
	logger, _ := zap.NewDevelopment()
	return logger.Sugar()
}

func (m *mockTestContext) Env() *types.Env {
	return nil
}

func (m *mockTestContext) Config() *config.Config {
	return nil
}

func (m *mockTestContext) Deployer() types.Deployer {
	return nil
}

func (m *mockTestContext) Workload() types.Workload {
	return nil
}

func (m *mockTestContext) Name() string {
	return "test"
}

func (m *mockTestContext) ManagementNamespace() string {
	return "default"
}

func (m *mockTestContext) AppNamespace() string {
	return "default"
}
