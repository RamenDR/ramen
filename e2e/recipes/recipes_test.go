// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

//nolint:testpackage
package recipes_test

import (
	"context"
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

const (
	ctxName         = "disapp-deploy-rbd"
	appNS           = "test-" + ctxName
	appName         = "busybox"
	depResourceType = "deployment"
)

var group = map[string]string{"group": "rg1"}

func TestGenerateWithNoHooks(t *testing.T) {
	recipeConfig := &config.Recipe{
		Type:      "generate",
		CheckHook: false,
		ExecHook:  false,
	}
	testContext := createTestContext(t, recipeConfig)

	actualRecipe := recipes.Generate(testContext, recipeConfig)

	expectedRecipe := &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Recipe",
			APIVersion: "recipe.ramendr.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ctxName,
			Namespace: appNS,
		},
		Spec: recipe.RecipeSpec{
			Groups: []*recipe.Group{
				{
					Name:      "rg1",
					Type:      "resource",
					BackupRef: "rg1",
					IncludedNamespaces: []string{
						appNS,
					},
					LabelSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "appname",
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{"busybox"},
							},
						},
					},
				},
			},
			Workflows: []*recipe.Workflow{
				{
					Name:     "backup",
					Sequence: []map[string]string{group},
				},
				{
					Name:     "restore",
					Sequence: []map[string]string{group},
				},
			},
		},
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
		ExecHook:  false,
	}

	expectedRecipe := getExpectedRecipeWithCheckHook()

	workload, err := createWorkload()
	if err != nil {
		t.Errorf("error creating workload")
	}

	deployer, err := createDeployer(recipeConfig)
	if err != nil {
		t.Errorf("error creating deployer")
	}

	parent := app.NewContext(context.Background(), &config.Config{}, &types.Env{}, zap.NewExample().Sugar())
	testContext := test.NewContext(parent, workload, deployer)

	actualRecipe := recipes.Generate(&testContext, recipeConfig)

	diff := helpers.UnifiedDiff(t, actualRecipe, expectedRecipe)
	if diff != "" {
		t.Fatalf("recipes are not equal: %s", diff)
	}
}

func TestGenerateWithOnlyExecHooks(t *testing.T) {
	recipeConfig := &config.Recipe{
		Type:      "generate",
		CheckHook: false,
		ExecHook:  true,
	}

	expectedRecipe := getExpectedRecipeWithExecHook()

	workload, err := createWorkload()
	if err != nil {
		t.Errorf("error creating workload")
	}

	deployer, err := createDeployer(recipeConfig)
	if err != nil {
		t.Errorf("error creating deployer")
	}

	parent := app.NewContext(context.Background(), &config.Config{}, &types.Env{}, zap.NewExample().Sugar())
	testContext := test.NewContext(parent, workload, deployer)

	actualRecipe := recipes.Generate(&testContext, recipeConfig)

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

	expectedRecipe := getExpectedRecipeWithCheckAndExecHook()

	workload, err := createWorkload()
	if err != nil {
		t.Errorf("error creating workload")
	}

	deployer, err := createDeployer(recipeConfig)
	if err != nil {
		t.Errorf("error creating deployer")
	}

	parent := app.NewContext(context.Background(), &config.Config{}, &types.Env{}, zap.NewExample().Sugar())
	testContext := test.NewContext(parent, workload, deployer)

	actualRecipe := recipes.Generate(&testContext, recipeConfig)

	diff := helpers.UnifiedDiff(t, actualRecipe, expectedRecipe)
	if diff != "" {
		t.Fatalf("recipes are not equal: %s", diff)
	}
}

//nolint:dupl
func getExpectedRecipeWithCheckHook() *recipe.Recipe {
	group := map[string]string{"group": "rg1"}
	checkHook := map[string]string{"hook": "check-hook/check-replicas"}

	return &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Recipe",
			APIVersion: "recipe.ramendr.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ctxName,
			Namespace: appNS,
		},
		Spec: recipe.RecipeSpec{
			Groups: []*recipe.Group{
				{
					Name:      "rg1",
					Type:      "resource",
					BackupRef: "rg1",
					IncludedNamespaces: []string{
						appNS,
					},
					LabelSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "appname",
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{"busybox"},
							},
						},
					},
				},
			},
			Hooks: []*recipe.Hook{
				{
					Name:           "check-hook",
					Type:           "check",
					Namespace:      appNS,
					NameSelector:   appName,
					SelectResource: depResourceType,
					Timeout:        300,
					Chks: []*recipe.Check{
						{
							Name:      "check-replicas",
							Condition: "{$.spec.replicas} == {$.status.readyReplicas}",
						},
					},
				},
			},
			Workflows: []*recipe.Workflow{
				{
					Name:     "backup",
					Sequence: []map[string]string{checkHook, group},
				},
				{
					Name:     "restore",
					Sequence: []map[string]string{group, checkHook},
				},
			},
		},
	}
}

//nolint:dupl
func getExpectedRecipeWithExecHook() *recipe.Recipe {
	group := map[string]string{"group": "rg1"}
	execHook := map[string]string{"hook": "exec-hook/ls"}

	return &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Recipe",
			APIVersion: "recipe.ramendr.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ctxName,
			Namespace: appNS,
		},
		Spec: recipe.RecipeSpec{
			Groups: []*recipe.Group{
				{
					Name:      "rg1",
					Type:      "resource",
					BackupRef: "rg1",
					IncludedNamespaces: []string{
						appNS,
					},
					LabelSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "appname",
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{"busybox"},
							},
						},
					},
				},
			},
			Hooks: []*recipe.Hook{
				{
					Name:           "exec-hook",
					Type:           "exec",
					Namespace:      appNS,
					NameSelector:   appName,
					SelectResource: depResourceType,
					Timeout:        300,
					Ops: []*recipe.Operation{
						{
							Name:    "ls",
							Command: "/bin/sh -c ls",
						},
					},
				},
			},
			Workflows: []*recipe.Workflow{
				{
					Name:     "backup",
					Sequence: []map[string]string{execHook, group},
				},
				{
					Name:     "restore",
					Sequence: []map[string]string{group, execHook},
				},
			},
		},
	}
}

//nolint:funlen
func getExpectedRecipeWithCheckAndExecHook() *recipe.Recipe {
	group := map[string]string{"group": "rg1"}
	execHook := map[string]string{"hook": "exec-hook/ls"}
	checkHook := map[string]string{"hook": "check-hook/check-replicas"}

	return &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Recipe",
			APIVersion: "recipe.ramendr.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ctxName,
			Namespace: appNS,
		},
		Spec: recipe.RecipeSpec{
			Groups: []*recipe.Group{
				{
					Name:      "rg1",
					Type:      "resource",
					BackupRef: "rg1",
					IncludedNamespaces: []string{
						appNS,
					},
					LabelSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "appname",
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{"busybox"},
							},
						},
					},
				},
			},
			Hooks: []*recipe.Hook{
				{
					Name:           "check-hook",
					Type:           "check",
					Namespace:      appNS,
					NameSelector:   appName,
					SelectResource: depResourceType,
					Timeout:        300,
					Chks: []*recipe.Check{
						{
							Name:      "check-replicas",
							Condition: "{$.spec.replicas} == {$.status.readyReplicas}",
						},
					},
				},
				{
					Name:           "exec-hook",
					Type:           "exec",
					Namespace:      appNS,
					NameSelector:   appName,
					SelectResource: depResourceType,
					Timeout:        300,
					Ops: []*recipe.Operation{
						{
							Name:    "ls",
							Command: "/bin/sh -c ls",
						},
					},
				},
			},
			Workflows: []*recipe.Workflow{
				{
					Name:     "backup",
					Sequence: []map[string]string{checkHook, execHook, group},
				},
				{
					Name:     "restore",
					Sequence: []map[string]string{group, checkHook, execHook},
				},
			},
		},
	}
}

// Helpers

func createTestContext(t *testing.T, rc *config.Recipe) types.TestContext {
	t.Helper()

	workload, err := createWorkload()
	if err != nil {
		t.Fatalf("error creating workload")
	}

	deployer, err := createDeployer(rc)
	if err != nil {
		t.Fatalf("error creating deployer")
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
