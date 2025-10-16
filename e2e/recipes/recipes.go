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
	appNamespace := ctx.AppNamespace()
	appName := ctx.Workload().GetAppName()
	resourceType := ctx.Workload().GetResourceType()

	recipe := prepareBaseRecipe(ctxName, appNamespace)

	recipe.Spec.Hooks = generateHook(appNamespace, appName, resourceType, recipeConfig)
	recipe.Spec.Groups = prepareGroups(appNamespace)
	recipe.Spec.Workflows = generateWorkflows(recipeConfig)

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

func generateHook(namespace, appName, resourceType string, rc *config.Recipe) []*recipe.Hook {
	var hooks []*recipe.Hook

	if rc.CheckHook {
		checkHook := prepareCheckHook(namespace, appName, resourceType)
		hooks = append(hooks, checkHook)
	}

	if rc.ExecHook {
		execHook := prepareExecHook(namespace, appName, resourceType)
		hooks = append(hooks, execHook)
	}

	return hooks
}

// XXX document in e2e.doc need for RBAC for specific resources
func prepareCheckHook(appNamespace, appName, resourceType string) *recipe.Hook {
	return &recipe.Hook{
		Name:           "check-hook",
		Type:           "check",
		Namespace:      appNamespace,
		NameSelector:   appName,      // XXX use label selecotr so we test the code path in ramen
		SelectResource: resourceType, // XXX fail if we cannot handle the resource typoe
		Timeout:        defaultHookTimeout,
		Chks: []*recipe.Check{
			{
				Name:      "check-replicas",
				Condition: "{$.spec.replicas} == {$.status.readyReplicas}", // XXX works only for resources with .replicas
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

func prepareGroups(appNamespace string) []*recipe.Group {
	return []*recipe.Group{
		{
			Name:      "rg1",
			Type:      "resource", // XXX make constant
			BackupRef: "rg1",
			IncludedNamespaces: []string{
				appNamespace,
			},
			LabelSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "appname", // XXX workload.LabelName()
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{"busybox"}, // XXX workload.GetAppName()},
					},
				},
			},
		},
	}
}

func generateWorkflows(recipeSpec *config.Recipe) []*recipe.Workflow {
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
