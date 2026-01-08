// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package recipes_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	recipe "github.com/ramendr/recipe/api/v1alpha1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/ramendr/ramen/e2e/app"
	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/helpers"
	"github.com/ramendr/ramen/e2e/recipes"
	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/workloads"
)

func TestGenerateWithNoHooks(t *testing.T) {
	recipeConfig := &config.Recipe{
		Type: "generate",
	}
	testContext := createTestContext(t, recipeConfig)

	actualRecipe, err := recipes.Generate(testContext, recipeConfig)
	if err != nil {
		t.Fatalf("error generating recipe: %v", err)
	}

	expectedRecipe := loadExpectedRecipe(t, "generate-no-hooks.yaml")

	diff := helpers.UnifiedDiff(t, actualRecipe, expectedRecipe)
	if diff != "" {
		t.Fatalf("recipes are not equal: %s", diff)
	}
}

func TestGenerateWithOnlyCheckHooks(t *testing.T) {
	recipeConfig := &config.Recipe{
		Type:      "generate",
		CheckHook: true,
	}
	testContext := createTestContext(t, recipeConfig)

	actualRecipe, err := recipes.Generate(testContext, recipeConfig)
	if err != nil {
		t.Fatalf("error generating recipe: %v", err)
	}

	expectedRecipe := loadExpectedRecipe(t, "generate-only-check-hooks.yaml")

	diff := helpers.UnifiedDiff(t, actualRecipe, expectedRecipe)
	if diff != "" {
		t.Fatalf("recipes are not equal: %s", diff)
	}
}

func TestGenerateWithOnlyExecHooks(t *testing.T) {
	recipeConfig := &config.Recipe{
		Type:     "generate",
		ExecHook: true,
	}
	testContext := createTestContext(t, recipeConfig)

	actualRecipe, err := recipes.Generate(testContext, recipeConfig)
	if err != nil {
		t.Fatalf("error generating recipe: %v", err)
	}

	expectedRecipe := loadExpectedRecipe(t, "generate-only-exec-hooks.yaml")

	diff := helpers.UnifiedDiff(t, actualRecipe, expectedRecipe)
	if diff != "" {
		t.Fatalf("recipes are not equal: %s", diff)
	}
}

func TestGenerateWithCheckAndExecHooks(t *testing.T) {
	recipeConfig := &config.Recipe{
		Type:      "generate",
		CheckHook: true,
		ExecHook:  true,
	}
	testContext := createTestContext(t, recipeConfig)

	actualRecipe, err := recipes.Generate(testContext, recipeConfig)
	if err != nil {
		t.Fatalf("error generating recipe: %v", err)
	}

	expectedRecipe := loadExpectedRecipe(t, "generate-check-and-exec-hooks.yaml")

	diff := helpers.UnifiedDiff(t, actualRecipe, expectedRecipe)
	if diff != "" {
		t.Fatalf("recipes are not equal: %s", diff)
	}
}

func TestGenerateWithNoChecks(t *testing.T) {
	recipeConfig := &config.Recipe{
		Type:      "generate",
		CheckHook: true,
	}

	workload := &NoHooks{}

	deploy, err := createDeployer(recipeConfig)
	if err != nil {
		t.Fatalf("error creating deployer: %v", err)
	}

	parent := app.NewContext(context.Background(), &config.Config{}, &types.Env{}, zap.NewExample().Sugar())
	testContext := test.NewContext(parent, workload, deploy)

	_, err = recipes.Generate(&testContext, recipeConfig)
	if err == nil {
		t.Fatalf("expected error when generating recipe with nil checks and operations, got nil")
	}

	if !errors.Is(err, recipes.ErrorUnsupported) {
		t.Fatalf("expected error %v, got %v", recipes.ErrorUnsupported, err)
	}
}

func TestGenerateWithNoOperations(t *testing.T) {
	recipeConfig := &config.Recipe{
		Type:     "generate",
		ExecHook: true,
	}

	workload := &NoHooks{}

	deploy, err := createDeployer(recipeConfig)
	if err != nil {
		t.Fatalf("error creating deployer: %v", err)
	}

	parent := app.NewContext(context.Background(), &config.Config{}, &types.Env{}, zap.NewExample().Sugar())
	testContext := test.NewContext(parent, workload, deploy)

	_, err = recipes.Generate(&testContext, recipeConfig)
	if err == nil {
		t.Fatalf("expected error when generating recipe with nil checks and operations, got nil")
	}

	if !errors.Is(err, recipes.ErrorUnsupported) {
		t.Fatalf("expected error %v, got %v", recipes.ErrorUnsupported, err)
	}
}

// workload interfaces and implementations

type NoHooks struct{}

var _ types.Workload = &NoHooks{}

func (w *NoHooks) GetAppName() string { return "nohooks-app" }

func (w *NoHooks) GetName() string { return "nohooks-workload" }

func (w *NoHooks) GetPath() string { return "path/to/nohooks" }

func (w *NoHooks) GetBranch() string { return "main" }

func (w *NoHooks) GetSelectResource() string { return "deployment" }

func (w *NoHooks) GetLabelSelector() *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: map[string]string{"appname": "nohooks-app"}}
}

func (w *NoHooks) GetChecks(namespace string) []*recipe.Check { return nil }

func (w *NoHooks) GetOperations(namespace string) []*recipe.Operation { return nil }

func (w *NoHooks) Kustomize() string { return "" }

func (w *NoHooks) Health(ctx types.TestContext, cluster *types.Cluster) error { return nil }

func (w *NoHooks) Status(ctx types.TestContext) ([]types.WorkloadStatus, error) { return nil, nil }

// Helpers

func loadExpectedRecipe(t *testing.T, filename string) *recipe.Recipe {
	t.Helper()

	path := filepath.Join("testdata", filename)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("error reading expected recipe file %q: %v", path, err)
	}

	r := &recipe.Recipe{}
	if err := yaml.Unmarshal(data, r); err != nil {
		t.Fatalf("error unmarshaling expected recipe from %q: %v", path, err)
	}

	return r
}

func createTestContext(t *testing.T, rc *config.Recipe) types.TestContext {
	t.Helper()

	workload, err := createWorkload()
	if err != nil {
		t.Fatalf("error creating workload: %v", err)
	}

	deployer, err := createDeployer(rc)
	if err != nil {
		t.Fatalf("error creating deployer: %v", err)
	}

	parent := app.NewContext(context.Background(), &config.Config{}, &types.Env{}, zap.NewExample().Sugar())
	tc := test.NewContext(parent, workload, deployer)

	return &tc
}

func createWorkload() (types.Workload, error) {
	pvcSpec := config.PVCSpec{
		Name:             "rbd",
		StorageClassName: "test-sc",
		AccessModes:      "ReadWriteOnce",
	}

	return workloads.New("deploy", "main", pvcSpec)
}

func createDeployer(rc *config.Recipe) (types.Deployer, error) {
	deployConfig := config.Deployer{
		Name:        "disapp",
		Type:        "disapp",
		Description: "Discovered apps application test",
		Recipe:      rc,
	}

	return deployers.New(deployConfig)
}
