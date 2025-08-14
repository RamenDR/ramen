// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package recipes

import (
	"context"
	"fmt"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func CreateRecipe(ctx types.TestContext, recipeSpec *config.Recipe) error {
	log := ctx.Logger()

	recipeC1 := prepareBaseRecipe(ctx)
	ns := ctx.AppNamespace()

	if recipeSpec.CheckHook || recipeSpec.ExecHook {
		recipeC1.Spec.Hooks = prepareHooks(ns)
	}

	recipeC1.Spec.Groups = prepareGroups(ns)
	recipeC1.Spec.Workflows = prepareWorkflows(recipeSpec)

	recipeC2 := recipeC1.DeepCopy()

	err := CreateRecipeOn("c1", ctx, recipeC1)
	if err != nil {
		return fmt.Errorf("Failed to create recipe %s in namespace %s on c2 cluster: %v", recipeC1.Name,
			recipeC1.Namespace, err)
	}

	err = CreateRecipeOn("c2", ctx, recipeC2)
	if err != nil {
		return fmt.Errorf("Failed to create recipe %s in namespace %s on c2 cluster: %v", recipeC1.Name,
			recipeC1.Namespace, err)
	}

	log.Debugf("Recipe %s-recipe created for discovered app \"%s/%s\"",
		ctx.Name(), ctx.AppNamespace(), ctx.Workload().GetAppName())

	return nil
}

func CreateRecipeOn(cluster string, ctx types.TestContext, recipe *recipe.Recipe) error {
	var client client.Client

	if cluster == "c1" {
		client = ctx.Env().C1.Client
	}

	if cluster == "c2" {
		client = ctx.Env().C2.Client
	}

	return client.Create(context.Background(), recipe)
}

func DeleteRecipe(ctx types.TestContext) error {
	if err := deleteRecipeOn("c1", ctx); err != nil {
		return fmt.Errorf("failed to delete recipe on c1: %w", err)
	}

	if err := deleteRecipeOn("c2", ctx); err != nil {
		return fmt.Errorf("failed to delete recipe on c2: %w", err)
	}

	return nil
}

func deleteRecipeOn(cluster string, ctx types.TestContext) error {
	var client client.Client

	if cluster == "c1" {
		client = ctx.Env().C1.Client
	}

	if cluster == "c2" {
		client = ctx.Env().C2.Client
	}

	r := &recipe.Recipe{}
	key := k8stypes.NamespacedName{
		Name:      ctx.Name() + "-recipe",
		Namespace: ctx.AppNamespace(),
	}

	err := client.Get(ctx.Context(), key, r)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		return nil // Recipe not found, nothing to delete
	}

	return client.Delete(ctx.Context(), r)
}

func prepareBaseRecipe(ctx types.TestContext) *recipe.Recipe {
	return &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Recipe",
			APIVersion: "recipe.ramendr.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ctx.Name() + "-recipe",
			Namespace: ctx.AppNamespace(),
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
func prepareHooks(namespace string) []*recipe.Hook {
	return []*recipe.Hook{
		{
			Name:           "check-hook",
			Type:           "check",
			Namespace:      namespace,
			NameSelector:   "busybox",
			SelectResource: "deployment",
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
			Namespace:      namespace,
			NameSelector:   "busybox",
			SelectResource: "deployment",
			Timeout:        300,
			Ops: []*recipe.Operation{
				{
					Name:    "ls",
					Command: "/bin/sh -c ls",
				},
			},
		},
	}
}

func prepareWorkflow(name string) *recipe.Workflow {
	return &recipe.Workflow{
		Name: name,
		Sequence: []map[string]string{
			{
				"group": "rg1",
			},
		},
	}
}

func prepareWorkflows(recipeSpec *config.Recipe) []*recipe.Workflow {
	backup := prepareWorkflow("backup")
	restore := prepareWorkflow("restore")

	checkHook := map[string]string{
		"hook": "check-hook/check-replicas",
	}

	execHook := map[string]string{
		"hook": "exec-hook/ls",
	}

	group := map[string]string{
		"group": "rg1",
	}

	if recipeSpec.ExecHook && recipeSpec.CheckHook {
		addCheckAndExecHooks(backup, checkHook, execHook, group)
		addCheckAndExecHooks(restore, checkHook, execHook, group)

		return []*recipe.Workflow{
			backup,
			restore,
		}
	}

	if recipeSpec.CheckHook {
		addCheckHooks(backup, checkHook, group)
		addCheckHooks(restore, checkHook, group)

		return []*recipe.Workflow{
			backup,
			restore,
		}
	}

	if recipeSpec.ExecHook {
		addExecHooks(backup, execHook, group)
		addExecHooks(restore, execHook, group)
	}

	return []*recipe.Workflow{
		backup,
		restore,
	}
}

// nolint:mnd
func addCheckHooks(workflow *recipe.Workflow, checkHook, group map[string]string) {
	seq := make([]map[string]string, 2)

	if workflow.Name == "backup" {
		seq[0] = checkHook

		seq[1] = group
	}

	if workflow.Name == "restore" {
		seq[0] = group

		seq[1] = checkHook
	}

	workflow.Sequence = seq
}

// nolint:mnd
func addExecHooks(workflow *recipe.Workflow, execHook, group map[string]string) {
	seq := make([]map[string]string, 2)

	if workflow.Name == "backup" {
		seq[0] = execHook
		seq[1] = group
	}

	if workflow.Name == "restore" {
		seq[0] = group
		seq[1] = execHook
	}

	workflow.Sequence = seq
}

// nolint:mnd
func addCheckAndExecHooks(workflow *recipe.Workflow, checkHook, execHook, group map[string]string) {
	seq := make([]map[string]string, 3)

	if workflow.Name == "backup" {
		seq[0] = checkHook
		seq[1] = execHook
		seq[2] = group
	}

	if workflow.Name == "restore" {
		seq[0] = group
		seq[1] = checkHook
		seq[2] = execHook
	}

	workflow.Sequence = seq
}
