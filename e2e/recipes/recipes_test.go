// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package recipes_test

import (
	"context"
	"errors"
	"testing"

	recipe "github.com/ramendr/recipe/api/v1alpha1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

	expectedRecipe := &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Recipe",
			APIVersion: "recipe.ramendr.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      testContext.Name(),
			Namespace: testContext.AppNamespace(),
		},
		Spec: recipe.RecipeSpec{
			Groups: []*recipe.Group{
				{
					Name:      "rg1",
					Type:      "resource",
					BackupRef: "rg1",
					IncludedNamespaces: []string{
						testContext.AppNamespace(),
					},
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"appname": "busybox",
						},
					},
				},
			},
			Workflows: []*recipe.Workflow{
				{
					Name:     "backup",
					Sequence: []map[string]string{{"group": "rg1"}},
				},
				{
					Name:     "restore",
					Sequence: []map[string]string{{"group": "rg1"}},
				},
			},
		},
	}

	actualRecipe, err := recipes.Generate(testContext, recipeConfig)
	if err != nil {
		t.Fatalf("error generating recipe: %v", err)
	}

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

	group := map[string]string{"group": "rg1"}
	replicasCheckHook := map[string]string{"hook": "check-hook/check-replicas"}
	availableCheckHook := map[string]string{"hook": "check-hook/check-available"}

	expectedRecipe := &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Recipe",
			APIVersion: "recipe.ramendr.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      testContext.Name(),
			Namespace: testContext.AppNamespace(),
		},
		Spec: recipe.RecipeSpec{
			Groups: []*recipe.Group{
				{
					Name:      "rg1",
					Type:      "resource",
					BackupRef: "rg1",
					IncludedNamespaces: []string{
						testContext.AppNamespace(),
					},
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"appname": "busybox",
						},
					},
				},
			},
			Hooks: []*recipe.Hook{
				{
					Name:      "check-hook",
					Type:      "check",
					Namespace: testContext.AppNamespace(),
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"appname": "busybox",
						},
					},
					SelectResource: testContext.Workload().GetSelectResource(),
					Timeout:        300,
					Chks: []*recipe.Check{
						{
							Name:      "check-replicas",
							Condition: "{$.spec.replicas} == {$.status.readyReplicas}",
						},
						{
							Name:      "check-available",
							Condition: "{$.spec.replicas} == {$.status.availableReplicas}",
						},
					},
				},
			},
			Workflows: []*recipe.Workflow{
				{
					Name:     "backup",
					Sequence: []map[string]string{replicasCheckHook, availableCheckHook, group},
				},
				{
					Name:     "restore",
					Sequence: []map[string]string{group, replicasCheckHook, availableCheckHook},
				},
			},
		},
	}

	actualRecipe, err := recipes.Generate(testContext, recipeConfig)
	if err != nil {
		t.Fatalf("error generating recipe: %v", err)
	}

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

	group := map[string]string{"group": "rg1"}
	lsExecHook := map[string]string{"hook": "exec-hook/ls"}
	echoExecHook := map[string]string{"hook": "exec-hook/echo"}

	expectedRecipe := &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Recipe",
			APIVersion: "recipe.ramendr.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      testContext.Name(),
			Namespace: testContext.AppNamespace(),
		},
		Spec: recipe.RecipeSpec{
			Groups: []*recipe.Group{
				{
					Name:      "rg1",
					Type:      "resource",
					BackupRef: "rg1",
					IncludedNamespaces: []string{
						testContext.AppNamespace(),
					},
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"appname": "busybox",
						},
					},
				},
			},
			Hooks: []*recipe.Hook{
				{
					Name:           "exec-hook",
					Type:           "exec",
					Namespace:      testContext.AppNamespace(),
					NameSelector:   testContext.Workload().GetAppName(),
					SelectResource: testContext.Workload().GetSelectResource(),
					Timeout:        300,
					Ops: []*recipe.Operation{
						{
							Name:    "ls",
							Command: "/bin/sh -c ls",
						},
						{
							Name:    "echo",
							Command: "/bin/sh -c echo 'Hello'",
						},
					},
				},
			},
			Workflows: []*recipe.Workflow{
				{
					Name:     "backup",
					Sequence: []map[string]string{lsExecHook, echoExecHook, group},
				},
				{
					Name:     "restore",
					Sequence: []map[string]string{group, lsExecHook, echoExecHook},
				},
			},
		},
	}

	actualRecipe, err := recipes.Generate(testContext, recipeConfig)
	if err != nil {
		t.Fatalf("error generating recipe: %v", err)
	}

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

	group := map[string]string{"group": "rg1"}
	lsExecHook := map[string]string{"hook": "exec-hook/ls"}
	echoExecHook := map[string]string{"hook": "exec-hook/echo"}
	replicasCheckHook := map[string]string{"hook": "check-hook/check-replicas"}
	availableCheckHook := map[string]string{"hook": "check-hook/check-available"}

	expectedRecipe := &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Recipe",
			APIVersion: "recipe.ramendr.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      testContext.Name(),
			Namespace: testContext.AppNamespace(),
		},
		Spec: recipe.RecipeSpec{
			Groups: []*recipe.Group{
				{
					Name:      "rg1",
					Type:      "resource",
					BackupRef: "rg1",
					IncludedNamespaces: []string{
						testContext.AppNamespace(),
					},
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"appname": "busybox",
						},
					},
				},
			},
			Hooks: []*recipe.Hook{
				{
					Name:      "check-hook",
					Type:      "check",
					Namespace: testContext.AppNamespace(),
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"appname": "busybox",
						},
					},
					SelectResource: testContext.Workload().GetSelectResource(),
					Timeout:        300,
					Chks: []*recipe.Check{
						{
							Name:      "check-replicas",
							Condition: "{$.spec.replicas} == {$.status.readyReplicas}",
						},
						{
							Name:      "check-available",
							Condition: "{$.spec.replicas} == {$.status.availableReplicas}",
						},
					},
				},
				{
					Name:           "exec-hook",
					Type:           "exec",
					Namespace:      testContext.AppNamespace(),
					NameSelector:   testContext.Workload().GetAppName(),
					SelectResource: testContext.Workload().GetSelectResource(),
					Timeout:        300,
					Ops: []*recipe.Operation{
						{
							Name:    "ls",
							Command: "/bin/sh -c ls",
						},
						{
							Name:    "echo",
							Command: "/bin/sh -c echo 'Hello'",
						},
					},
				},
			},
			Workflows: []*recipe.Workflow{
				{
					Name: "backup",
					Sequence: []map[string]string{
						replicasCheckHook,
						availableCheckHook,
						lsExecHook,
						echoExecHook,
						group,
					},
				},
				{
					Name: "restore",
					Sequence: []map[string]string{
						group,
						lsExecHook,
						echoExecHook,
						replicasCheckHook,
						availableCheckHook,
					},
				},
			},
		},
	}

	actualRecipe, err := recipes.Generate(testContext, recipeConfig)
	if err != nil {
		t.Fatalf("error generating recipe: %v", err)
	}

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
		Name:             "busybox-pvc",
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
