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
	// Constant serves as the default timeout for the resources to come up when executing hook.
	defaultHookTimeout = 300
	resource           = "resource"
)

func Generate(ctx types.TestContext, recipeConfig *config.Recipe) *recipe.Recipe {
	ctxName := ctx.Name()
	appNamespace := ctx.AppNamespace()

	return &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Recipe",
			APIVersion: "recipe.ramendr.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ctxName,
			Namespace: appNamespace,
		},
		Spec: recipe.RecipeSpec{
			Hooks:     prepareHooks(ctx, recipeConfig),
			Groups:    prepareGroups(ctx, appNamespace),
			Workflows: generateWorkflows(recipeConfig),
		},
	}
}

func prepareHooks(ctx types.TestContext, rc *config.Recipe) []*recipe.Hook {
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

// XXX document in e2e.doc need for RBAC for specific resources
func prepareCheckHook(ctx types.TestContext) *recipe.Hook {
	return &recipe.Hook{
		Name:      "check-hook",
		Type:      "check",
		Namespace: ctx.AppNamespace(),
		LabelSelector: &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      ctx.Workload().GetLabelName(),
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{ctx.Workload().GetAppName()},
				},
			},
		},
		SelectResource: ctx.Workload().GetResourceType(),
		Timeout:        defaultHookTimeout,
		Chks: []*recipe.Check{
			{
				Name:      "check-replicas",
				Condition: "{$.spec.replicas} == {$.status.readyReplicas}", // XXX works only for resources with .replicas
			},
		},
	}
}

func prepareExecHook(ctx types.TestContext) *recipe.Hook {
	return &recipe.Hook{
		Name:           "exec-hook",
		Type:           "exec",
		Namespace:      ctx.AppNamespace(),
		NameSelector:   ctx.Workload().GetAppName(),
		SelectResource: ctx.Workload().GetResourceType(),
		Timeout:        defaultHookTimeout,
		Ops: []*recipe.Operation{
			{
				Name:    "ls",
				Command: "/bin/sh -c ls",
			},
		},
	}
}

func prepareGroups(ctx types.TestContext, appNamespace string) []*recipe.Group {
	return []*recipe.Group{
		{
			Name:      "rg1",
			Type:      resource,
			BackupRef: "rg1",
			IncludedNamespaces: []string{
				appNamespace,
			},
			LabelSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      ctx.Workload().GetLabelName(),
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{ctx.Workload().GetAppName()},
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
