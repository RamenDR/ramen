// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package recipes

import (
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
)

// Constant serves as the default timeout for the resources to come up when executing hook.
const defaultHookTimeout = 300

func Generate(ctx types.TestContext, recipeConfig *config.Recipe) *recipe.Recipe {
	ctxName := ctx.Name()
	appNS := ctx.AppNamespace()
	appName := ctx.Workload().GetAppName()
	resourceType := ctx.Workload().GetResourceType()

	recipe := prepareBaseRecipe(ctxName, appNS)

	if recipeConfig.CheckHook || recipeConfig.ExecHook {
		recipe.Spec.Hooks = prepareHooks(appNS, appName, resourceType, recipeConfig.CheckHook, recipeConfig.ExecHook)
	}

	recipe.Spec.Groups = prepareGroups(appNS)
	recipe.Spec.Workflows = prepareWorkflows(recipeConfig)

	return recipe
}

func prepareBaseRecipe(ctxName, appNS string) *recipe.Recipe {
	return &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Recipe",
			APIVersion: "recipe.ramendr.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ctxName,
			Namespace: appNS,
		},
	}
}

func prepareGroups(namespace string) []*recipe.Group {
	return []*recipe.Group{
		{
			Name:      "rg1",
			Type:      "resource",
			BackupRef: "rg1",
			IncludedNamespaces: []string{
				namespace,
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
	}
}

// nolint:mnd
func prepareHooks(namespace, appName, resourceType string, checkHook, execHook bool) []*recipe.Hook {
	hooks := make([]*recipe.Hook, 0)

	if checkHook {
		cHook := prepareCheckHook(namespace, appName, resourceType)
		hooks = append(hooks, cHook)
	}

	if execHook {
		eHook := prepareExecHook(namespace, appName, resourceType)
		hooks = append(hooks, eHook)
	}

	return hooks
}

func prepareCheckHook(namespace, appName, resourceType string) *recipe.Hook {
	return &recipe.Hook{
		Name:           "check-hook",
		Type:           "check",
		Namespace:      namespace,
		NameSelector:   appName,
		SelectResource: resourceType,
		Timeout:        defaultHookTimeout,
		Chks: []*recipe.Check{
			{
				Name:      "check-replicas",
				Condition: "{$.spec.replicas} == {$.status.readyReplicas}",
			},
		},
	}
}

func prepareExecHook(namespace, appName, resourceType string) *recipe.Hook {
	return &recipe.Hook{
		Name:           "exec-hook",
		Type:           "exec",
		Namespace:      namespace,
		NameSelector:   appName,
		SelectResource: resourceType,
		Timeout:        defaultHookTimeout,
		Ops: []*recipe.Operation{
			{
				Name:    "ls",
				Command: "/bin/sh -c ls",
			},
		},
	}
}

func prepareWorkflows(recipeSpec *config.Recipe) []*recipe.Workflow {
	backup := &recipe.Workflow{Name: "backup"}
	restore := &recipe.Workflow{Name: "restore"}

	checkHook := map[string]string{"hook": "check-hook/check-replicas"}
	execHook := map[string]string{"hook": "exec-hook/ls"}
	group := map[string]string{"group": "rg1"}

	switch {
	case recipeSpec.ExecHook && recipeSpec.CheckHook:
		backup.Sequence = []map[string]string{checkHook, execHook, group}
		restore.Sequence = []map[string]string{group, checkHook, execHook}
	case !recipeSpec.ExecHook && recipeSpec.CheckHook:
		backup.Sequence = []map[string]string{checkHook, group}
		restore.Sequence = []map[string]string{group, checkHook}
	case recipeSpec.ExecHook && !recipeSpec.CheckHook:
		backup.Sequence = []map[string]string{execHook, group}
		restore.Sequence = []map[string]string{group, execHook}
	case !recipeSpec.ExecHook && !recipeSpec.CheckHook:
		backup.Sequence = []map[string]string{group}
		restore.Sequence = []map[string]string{group}
	}

	return []*recipe.Workflow{backup, restore}
}
