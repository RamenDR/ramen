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

type recipeElements struct {
	pvcSelector     PvcSelector
	captureWorkflow []kubeobjects.CaptureSpec
	recoverWorkflow []kubeobjects.RecoverSpec
}

func captureWorkflowDefault() []kubeobjects.CaptureSpec { return []kubeobjects.CaptureSpec{{}} }
func recoverWorkflowDefault() []kubeobjects.RecoverSpec { return []kubeobjects.RecoverSpec{{}} }

func GetPVCSelector(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	log logr.Logger,
) (PvcSelector, error) {
	recipeElements, err := recipeVolumesAndOptionallyWorkflowsGet(ctx, reader, vrg, log,
		func(recipe.Recipe, *recipeElements) error { return nil },
	)

	return recipeElements.pvcSelector, err
}

func recipeElementsGet(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	log logr.Logger,
) (recipeElements, error) {
	return recipeVolumesAndOptionallyWorkflowsGet(ctx, reader, vrg, log, recipeWorkflowsGet)
}

func recipeVolumesAndOptionallyWorkflowsGet(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	log logr.Logger, workflowsGet func(recipe.Recipe, *recipeElements) error,
) (recipeElements, error) {
	if vrg.Spec.KubeObjectProtection == nil || vrg.Spec.KubeObjectProtection.RecipeRef == nil {
		return recipeElements{
			pvcSelector:     pvcSelectorDefault(vrg),
			captureWorkflow: captureWorkflowDefault(),
			recoverWorkflow: recoverWorkflowDefault(),
		}, nil
	}

	recipe := recipe.Recipe{}
	if err := reader.Get(ctx, types.NamespacedName{
		Namespace: vrg.Namespace,
		Name:      vrg.Spec.KubeObjectProtection.RecipeRef.Name,
	}, &recipe); err != nil {
		return recipeElements{}, errors.Wrap(err, "recipe get")
	}

	if err := recipeParametersExpand(&recipe, vrg.Spec.KubeObjectProtection.RecipeParameters); err != nil {
		return recipeElements{}, errors.Wrap(err, "recipe parameters expand")
	}

	recipeElements := recipeElements{
		pvcSelector: pvcSelectorRecipeRefNonNil(recipe, vrg),
	}

	return recipeElements, workflowsGet(recipe, &recipeElements)
}

func recipeWorkflowsGet(recipe recipe.Recipe, recipeElements *recipeElements) error {
	var err error

	recipeElements.captureWorkflow, err = getCaptureGroups(recipe)
	if err != nil {
		return errors.Wrap(err, "Failed to get groups from capture workflow")
	}

	recipeElements.recoverWorkflow, err = getRecoverGroups(recipe)
	if err != nil {
		return errors.Wrap(err, "Failed to get groups from recovery workflow")
	}

	return err
}

func recipeParametersExpand(recipe *recipe.Recipe, parameters map[string][]string) error {
	b, err := json.Marshal(*recipe)
	if err != nil {
		return err
	}

	if err = json.Unmarshal([]byte(parametersExpand(string(b), parameters)), recipe); err != nil {
		return err
	}

	return nil
}

func parametersExpand(s string, parameters map[string][]string) string {
	return os.Expand(s, func(s string) string {
		values := parameters[s]

		return strings.Join(values, ",")
	})
}
