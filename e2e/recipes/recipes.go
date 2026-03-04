// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package recipes

import (
	"fmt"
	"maps"
	"slices"

	recipe "github.com/ramendr/recipe/api/v1alpha1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
)

type Error string

const ErrorUnsupported = Error("operation is not supported")

const (
	// defaultHookTimeout is the default timeout in seconds for the resources to come up when executing hook.
	defaultHookTimeout = 300

	// resource is the group type for Resource groups.
	resource = "resource"

	// kind is the Kind value for Recipe resources.
	kind = "Recipe"

	// apiVersion is the APIVersion value for Recipe resources.
	apiVersion = "recipe.ramendr.io/v1alpha1"

	defaultGroupName = "rg1"
	backup           = "backup"
	restore          = "restore"
)

// supportedSelectResources keeps supported select resources.
var supportedSelectResources = map[string]struct{}{
	"deployment":  {},
	"pod":         {},
	"statefulset": {},
}

func (e Error) Error() string {
	return string(e)
}

func Generate(ctx types.TestContext, recipeConfig *config.Recipe) (*recipe.Recipe, error) {
	ctxName := ctx.Name()
	appNamespace := ctx.AppNamespace()

	hooks, err := generateHooks(ctx, recipeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to generate hooks: %w", err)
	}

	return &recipe.Recipe{
		TypeMeta: metav1.TypeMeta{
			Kind:       kind,
			APIVersion: apiVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ctxName,
			Namespace: appNamespace,
		},
		Spec: recipe.RecipeSpec{
			Hooks:     hooks,
			Groups:    generateGroups(ctx, appNamespace),
			Workflows: generateWorkflows(hooks),
		},
	}, nil
}

func CreateRecipeOnManagedClusters(ctx types.TestContext, recipe *recipe.Recipe) error {
	env := ctx.Env()
	log := ctx.Logger()

	for _, cluster := range env.ManagedClusters() {
		recipeCopy := recipe.DeepCopy()

		err := cluster.Client.Create(ctx.Context(), recipeCopy)
		if err != nil {
			if !k8serrors.IsAlreadyExists(err) {
				return fmt.Errorf("failed to create Recipe %q in cluster %q: %w",
					recipe.Name, cluster.Name, err)
			}

			log.Debugf("Recipe %q already exists in cluster %q, skipping creation", recipe.Name, cluster.Name)

			continue
		}
	}

	return nil
}

func DeleteRecipeOnManagedClusters(ctx types.TestContext, r *recipe.Recipe) error {
	env := ctx.Env()
	log := ctx.Logger()

	for _, cluster := range env.ManagedClusters() {
		recipe := &recipe.Recipe{
			ObjectMeta: metav1.ObjectMeta{
				Name:      r.Name,
				Namespace: r.Namespace,
			},
		}

		err := cluster.Client.Delete(ctx.Context(), recipe)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				log.Debugf("recipe \"%s/%s\" not found in cluster %q", r.Namespace, r.Name, cluster.Name)

				continue
			}

			return fmt.Errorf("failed to delete Recipe %q in cluster %q: %w",
				recipe.Name, cluster.Name, err)
		}
	}

	log.Debugf("Recipe %q deleted from managed clusters", r.Name)

	return nil
}

func generateHooks(ctx types.TestContext, rc *config.Recipe) ([]*recipe.Hook, error) {
	var hooks []*recipe.Hook

	if rc.CheckHook {
		checkHook, err := prepareCheckHook(ctx)
		if err != nil {
			return nil, err
		}

		hooks = append(hooks, checkHook)
	}

	if rc.ExecHook {
		execHook, err := prepareExecHook(ctx)
		if err != nil {
			return nil, err
		}

		hooks = append(hooks, execHook)
	}

	return hooks, nil
}

func prepareCheckHook(ctx types.TestContext) (*recipe.Hook, error) {
	var checks []*recipe.Check
	if checks = ctx.Workload().GetChecks(ctx.AppNamespace()); len(checks) == 0 {
		return nil, fmt.Errorf("failed to prepare check hooks for workload %q: %w", ctx.Workload().GetName(),
			ErrorUnsupported)
	}

	selectResource, err := validateSelectResource(ctx.Workload())
	if err != nil {
		return nil, fmt.Errorf("failed to create check hook: %w", err)
	}

	return &recipe.Hook{
		Name:           "check-hook",
		Type:           "check",
		Namespace:      ctx.AppNamespace(),
		LabelSelector:  ctx.Workload().GetLabelSelector(),
		SelectResource: selectResource,
		Timeout:        defaultHookTimeout,
		Chks:           checks,
	}, nil
}

func prepareExecHook(ctx types.TestContext) (*recipe.Hook, error) {
	var operations []*recipe.Operation
	if operations = ctx.Workload().GetOperations(ctx.AppNamespace()); len(operations) == 0 {
		return nil, fmt.Errorf("failed to create exec hook for workload %q: %w", ctx.Workload().GetName(),
			ErrorUnsupported)
	}

	selectResource, err := validateSelectResource(ctx.Workload())
	if err != nil {
		return nil, fmt.Errorf("failed to create exec hook %w", err)
	}

	return &recipe.Hook{
		Name:           "exec-hook",
		Type:           "exec",
		Namespace:      ctx.AppNamespace(),
		NameSelector:   ctx.Workload().GetAppName(),
		SelectResource: selectResource,
		Timeout:        defaultHookTimeout,
		Ops:            operations,
	}, nil
}

func validateSelectResource(w types.Workload) (string, error) {
	selectResource := w.GetSelectResource()
	if _, ok := supportedSelectResources[selectResource]; !ok {
		return selectResource, fmt.Errorf("selectResource %q is not supported for workload %q (choose from %v)",
			selectResource, w.GetName(), slices.Collect(maps.Keys(supportedSelectResources)))
	}

	return selectResource, nil
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

func generateWorkflows(hooks []*recipe.Hook) []*recipe.Workflow {
	backup := &recipe.Workflow{Name: backup}
	restore := &recipe.Workflow{Name: restore}

	checkHooks, execHooks := collectHooks(hooks)
	group := map[string]string{"group": defaultGroupName}

	backup.Sequence = append(backup.Sequence, checkHooks...)
	backup.Sequence = append(backup.Sequence, execHooks...)
	backup.Sequence = append(backup.Sequence, group)

	restore.Sequence = append(restore.Sequence, group)
	restore.Sequence = append(restore.Sequence, execHooks...)
	restore.Sequence = append(restore.Sequence, checkHooks...)

	return []*recipe.Workflow{backup, restore}
}

func collectHooks(hooks []*recipe.Hook) (checkHooks, execHooks []map[string]string) {
	for _, hook := range hooks {
		switch hook.Type {
		case "check":
			for _, chk := range hook.Chks {
				checkHooks = append(checkHooks, map[string]string{"hook": hook.Name + "/" + chk.Name})
			}
		case "exec":
			for _, op := range hook.Ops {
				execHooks = append(execHooks, map[string]string{"hook": hook.Name + "/" + op.Name})
			}
		}
	}

	return
}
