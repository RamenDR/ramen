// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

//nolint:testpackage
package recipes_test

import (
	"context"
	"reflect"
	"testing"

	recipe "github.com/ramendr/recipe/api/v1alpha1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/deployers"
	e2e_recipe "github.com/ramendr/ramen/e2e/recipes"
	"github.com/ramendr/ramen/e2e/test"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/workloads"
)

const (
	ctxName         = "disapp-deploy-busybox-pvc"
	appNS           = ctxName
	appName         = "busybox"
	depResourceType = "deployment"
)

type Context struct {
	log     *zap.SugaredLogger
	env     *types.Env
	config  *config.Config
	context context.Context
}

func (c *Context) Logger() *zap.SugaredLogger {
	return c.log
}

func (c *Context) Config() *config.Config {
	return c.config
}

func (c *Context) Env() *types.Env {
	return c.env
}

func (c *Context) Context() context.Context {
	return c.context
}

func createWorkload() (types.Workload, error) {
	pvcSpec := config.PVCSpec{
		Name:             "busybox-pvc",
		StorageClassName: "test-sc",
		AccessModes:      "ReadWriteOnce",
	}

	return workloads.New("deploy", "main", pvcSpec)
}

func createDeployer() (types.Deployer, error) {
	deployConfig := config.Deployer{
		Name:        "disapp",
		Type:        "disapp",
		Description: "Discovered apps application test",
		Recipe: &config.Recipe{
			Type:      "generate",
			CheckHook: false,
			ExecHook:  false,
		},
	}

	return deployers.New(deployConfig)
}

func TestGenerateWithNoHooks(t *testing.T) {
	expectedRecipe := getExpectedRecipeWithoutHooks()

	workload, err := createWorkload()
	if err != nil {
		t.Errorf("error creating workload")
	}

	deployer, err := createDeployer()
	if err != nil {
		t.Errorf("error creating deployer")
	}

	parent := Context{
		log:     zap.NewExample().Sugar(),
		env:     &types.Env{},
		config:  &config.Config{},
		context: context.Background(),
	}
	testContext := test.NewContext(&parent, workload, deployer)

	recipeConfig := &config.Recipe{
		Type:      "generate",
		CheckHook: false,
		ExecHook:  false,
	}

	actualRecipe := e2e_recipe.Generate(&testContext, recipeConfig)
	if !reflect.DeepEqual(expectedRecipe, actualRecipe) {
		t.Errorf("actual generated recipe doesn't match with expected recipe")
	}
}

func TestGenerateWithOnlyCheckHooks(t *testing.T) {
	expectedRecipe := getExpectedRecipeWithCheckHook()

	workload, err := createWorkload()
	if err != nil {
		t.Errorf("error creating workload")
	}

	deployer, err := createDeployer()
	if err != nil {
		t.Errorf("error creating deployer")
	}

	parent := Context{
		log:     zap.NewExample().Sugar(),
		env:     &types.Env{},
		config:  &config.Config{},
		context: context.Background(),
	}
	testContext := test.NewContext(&parent, workload, deployer)

	recipeConfig := &config.Recipe{
		Type:      "generate",
		CheckHook: true,
		ExecHook:  false,
	}

	actualRecipe := e2e_recipe.Generate(&testContext, recipeConfig)
	if !reflect.DeepEqual(expectedRecipe, actualRecipe) {
		t.Errorf("actual generated recipe doesn't match with expected recipe")
	}
}

func TestGenerateWithOnlyExecHooks(t *testing.T) {
	expectedRecipe := getExpectedRecipeWithExecHook()

	workload, err := createWorkload()
	if err != nil {
		t.Errorf("error creating workload")
	}

	deployer, err := createDeployer()
	if err != nil {
		t.Errorf("error creating deployer")
	}

	parent := Context{
		log:     zap.NewExample().Sugar(),
		env:     &types.Env{},
		config:  &config.Config{},
		context: context.Background(),
	}
	testContext := test.NewContext(&parent, workload, deployer)

	recipeConfig := &config.Recipe{
		Type:      "generate",
		CheckHook: false,
		ExecHook:  true,
	}

	actualRecipe := e2e_recipe.Generate(&testContext, recipeConfig)
	if !reflect.DeepEqual(expectedRecipe, actualRecipe) {
		t.Errorf("actual generated recipe doesn't match with expected recipe")
	}
}

func TestGenerateWithCheckAndExecHooks(t *testing.T) {
	expectedRecipe := getExpectedRecipeWithCheckAndExecHook()

	workload, err := createWorkload()
	if err != nil {
		t.Errorf("error creating workload")
	}

	deployer, err := createDeployer()
	if err != nil {
		t.Errorf("error creating deployer")
	}

	parent := Context{
		log:     zap.NewExample().Sugar(),
		env:     &types.Env{},
		config:  &config.Config{},
		context: context.Background(),
	}
	testContext := test.NewContext(&parent, workload, deployer)

	recipeConfig := &config.Recipe{
		Type:      "generate",
		CheckHook: true,
		ExecHook:  true,
	}

	actualRecipe := e2e_recipe.Generate(&testContext, recipeConfig)
	if !reflect.DeepEqual(expectedRecipe, actualRecipe) {
		t.Errorf("actual generated recipe doesn't match with expected recipe")
	}
}

func getExpectedRecipeWithoutHooks() *recipe.Recipe {
	group := map[string]string{"group": "rg1"}

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
