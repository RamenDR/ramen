// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/kubeobjects"
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type RecipeElements struct {
	PvcSelector     PvcSelector
	CaptureWorkflow []kubeobjects.CaptureSpec
	RecoverWorkflow []kubeobjects.RecoverSpec
}

func captureWorkflowDefault() []kubeobjects.CaptureSpec { return []kubeobjects.CaptureSpec{{}} }
func recoverWorkflowDefault() []kubeobjects.RecoverSpec { return []kubeobjects.RecoverSpec{{}} }

func GetPVCSelector(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	log logr.Logger,
) (PvcSelector, error) {
	recipeElements, err := recipeVolumesAndOptionallyWorkflowsGet(ctx, reader, vrg, log,
		func(recipe.Recipe, *RecipeElements) error { return nil },
	)

	return recipeElements.PvcSelector, err
}

func RecipeElementsGet(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	log logr.Logger,
) (RecipeElements, error) {
	return recipeVolumesAndOptionallyWorkflowsGet(ctx, reader, vrg, log, recipeWorkflowsGet)
}

func recipeVolumesAndOptionallyWorkflowsGet(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	log logr.Logger, workflowsGet func(recipe.Recipe, *RecipeElements) error,
) (RecipeElements, error) {
	if vrg.Spec.KubeObjectProtection == nil || vrg.Spec.KubeObjectProtection.RecipeRef == nil {
		return RecipeElements{
			PvcSelector:     pvcSelectorDefault(vrg),
			CaptureWorkflow: captureWorkflowDefault(),
			RecoverWorkflow: recoverWorkflowDefault(),
		}, nil
	}

	recipe := recipe.Recipe{}
	if err := reader.Get(ctx, types.NamespacedName{
		Namespace: vrg.Namespace,
		Name:      vrg.Spec.KubeObjectProtection.RecipeRef.Name,
	}, &recipe); err != nil {
		return RecipeElements{}, errors.Wrap(err, "recipe get")
	}

	if err := RecipeParametersExpand(&recipe, vrg.Spec.KubeObjectProtection.RecipeParameters); err != nil {
		return RecipeElements{}, errors.Wrap(err, "recipe parameters expand")
	}

	recipeElements := RecipeElements{
		PvcSelector: pvcSelectorRecipeRefNonNil(recipe, vrg),
	}

	return recipeElements, workflowsGet(recipe, &recipeElements)
}

func recipeWorkflowsGet(recipe recipe.Recipe, recipeElements *RecipeElements) error {
	var err error

	recipeElements.CaptureWorkflow, err = getCaptureGroups(recipe)
	if err != nil {
		return errors.Wrap(err, "Failed to get groups from capture workflow")
	}

	recipeElements.RecoverWorkflow, err = getRecoverGroups(recipe)
	if err != nil {
		return errors.Wrap(err, "Failed to get groups from recovery workflow")
	}

	return err
}

func RecipeParametersExpand(recipe *recipe.Recipe, parameters map[string][]string) error {
	bytes, err := json.Marshal(*recipe)
	if err != nil {
		return err
	}

	if err = json.Unmarshal([]byte(parametersExpand(string(bytes), parameters)), recipe); err != nil {
		return err
	}

	return nil
}

func parametersExpand(s string, parameters map[string][]string) string {
	return os.Expand(s, func(s string) string {
		values := parameters[s]

		return strings.Join(values, `","`)
	})
}
