// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

//nolint:testpackage
package recipes

import (
	"reflect"
	"testing"

	"github.com/ramendr/ramen/e2e/config"
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ctxName         = "disapp-busybox-rbd"
	appNS           = "e2e-disapp-busybox-rbd"
	appName         = "busybox"
	depResourceType = "deployment"
)

type testingContext struct {
	name         string
	appNS        string
	appName      string
	resourceType string
}

func TestGenerate(t *testing.T) {
	tests := []struct {
		name         string
		recipeConfig *config.Recipe
		testingContext
		expectedRecipe *recipe.Recipe
	}{
		{
			name: "basicHooks",
			recipeConfig: &config.Recipe{
				Type:      "generate",
				CheckHook: false,
				ExecHook:  false,
			},
			testingContext: testingContext{
				name:         ctxName,
				appNS:        appNS,
				appName:      appName,
				resourceType: depResourceType,
			},
			expectedRecipe: getExpectedBasicRecipe(),
		},
		{
			name: "checkHooks",
			recipeConfig: &config.Recipe{
				Type:      "generate",
				CheckHook: true,
				ExecHook:  false,
			},
			testingContext: testingContext{
				name:         ctxName,
				appNS:        appNS,
				appName:      appName,
				resourceType: depResourceType,
			},
			expectedRecipe: getExpectedRecipeWithCheckHook(),
		},
		{
			name: "execHooks",
			recipeConfig: &config.Recipe{
				Type:      "generate",
				CheckHook: false,
				ExecHook:  true,
			},
			testingContext: testingContext{
				name:         ctxName,
				appNS:        appNS,
				appName:      appName,
				resourceType: depResourceType,
			},
			expectedRecipe: getExpectedRecipeWithExecHook(),
		},
		{
			name: "checkAndExecHooks",
			recipeConfig: &config.Recipe{
				Type:      "generate",
				CheckHook: true,
				ExecHook:  true,
			},
			testingContext: testingContext{
				name:         ctxName,
				appNS:        appNS,
				appName:      appName,
				resourceType: depResourceType,
			},
			expectedRecipe: getExpectedRecipeWithCheckAndExecHook(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualRecipe := Generate(tt.testingContext.name, tt.appNS, tt.appName, tt.resourceType, tt.recipeConfig)
			if !reflect.DeepEqual(tt.expectedRecipe, actualRecipe) {
				t.Errorf("actual generated recipe doesn't match with expected recipe")
			}
		})
	}
}

func getExpectedBasicRecipe() *recipe.Recipe {
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
