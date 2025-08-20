// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package recipes

import (
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
)

const (
	// DefaultHookTimeout is the default timeout in seconds for the resources to come up when executing hook.
	DefaultHookTimeout = 300
	// Resource is the group type for Resource groups.
	resource = "resource"
	// KindRecipe is the Kind value for Recipe resources.
	KindRecipe = "Recipe"
	// RecipeAPIVersion is the APIVersion value for Recipe resources.
	RecipeAPIVersion = "recipe.ramendr.io/v1alpha1"

	defaultGroupName = "rg1"
	backup           = "backup"
	restore          = "restore"
	hook             = "hook"
	group            = "group"
)

func Generate(ctx types.TestContext, recipeConfig *config.Recipe) *recipe.Recipe {
	ctxName := ctx.Name()
	appNamespace := ctx.AppNamespace()

	return &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       KindRecipe,
			APIVersion: RecipeAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ctxName,
			Namespace: appNamespace,
		},
		Spec: recipe.RecipeSpec{
			Hooks:     generateHooks(ctx, recipeConfig),
			Groups:    generateGroups(ctx, appNamespace),
			Workflows: generateWorkflows(recipeConfig),
		},
	}
}

func generateHooks(ctx types.TestContext, rc *config.Recipe) []*recipe.Hook {
	var hooks []*recipe.Hook

	if rc.CheckHook {
		checkHook := prepareCheckHook(ctx)
		hooks = append(hooks, checkHook)
	}

	if rc.ExecHook {
		execHook := prepareExecHook(ctx)
		hooks = append(hooks, execHook)
	}

	return hooks
}

func prepareCheckHook(ctx types.TestContext) *recipe.Hook {
	return &recipe.Hook{
		Name:           "check-hook",
		Type:           "check",
		Namespace:      ctx.AppNamespace(),
		LabelSelector:  ctx.Workload().GetLabelSelector(),
		SelectResource: ctx.Workload().GetSelectResource(),
		Timeout:        DefaultHookTimeout,
		Chks:           ctx.Workload().GetChecks(ctx.AppNamespace()),
	}
}

func prepareExecHook(ctx types.TestContext) *recipe.Hook {
	return &recipe.Hook{
		Name:           "exec-hook",
		Type:           "exec",
		Namespace:      ctx.AppNamespace(),
		NameSelector:   ctx.Workload().GetAppName(),
		SelectResource: ctx.Workload().GetSelectResource(),
		Timeout:        DefaultHookTimeout,
		Ops:            ctx.Workload().GetOperations(ctx.AppNamespace()),
	}
}

func generateGroups(ctx types.TestContext, appNamespace string) []*recipe.Group {
	return []*recipe.Group{
		{
			Name:      defaultGroupName,
			Type:      resource,
			BackupRef: defaultGroupName,
			IncludedNamespaces: []string{
				appNamespace,
			},
			LabelSelector: ctx.Workload().GetLabelSelector(),
		},
	}
}

func generateWorkflows(recipeSpec *config.Recipe) []*recipe.Workflow {
	backup := &recipe.Workflow{Name: backup}
	restore := &recipe.Workflow{Name: restore}

	checkHook := map[string]string{"hook": "check-hook/check-replicas"}
	execHook := map[string]string{"hook": "exec-hook/ls"}
	group := map[string]string{group: defaultGroupName}

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
