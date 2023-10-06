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

func captureWorkflowDefault(vrg ramen.VolumeReplicationGroup) []kubeobjects.CaptureSpec {
	return []kubeobjects.CaptureSpec{
		{
			Spec: kubeobjects.Spec{
				KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
					IncludedNamespaces: []string{vrg.Namespace},
				},
			},
		},
	}
}

func recoverWorkflowDefault() []kubeobjects.RecoverSpec { return []kubeobjects.RecoverSpec{{}} }

func GetPVCSelector(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	log logr.Logger,
) (PvcSelector, error) {
	recipeElements, err := recipeVolumesAndOptionallyWorkflowsGet(ctx, reader, vrg, log,
		func(recipe.Recipe, *RecipeElements, ramen.VolumeReplicationGroup) error { return nil },
	)

	return recipeElements.PvcSelector, err
}

func RecipeElementsGet(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	log logr.Logger,
) (RecipeElements, error) {
	return recipeVolumesAndOptionallyWorkflowsGet(ctx, reader, vrg, log, recipeWorkflowsGet)
}

func recipeVolumesAndOptionallyWorkflowsGet(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	log logr.Logger, workflowsGet func(recipe.Recipe, *RecipeElements, ramen.VolumeReplicationGroup) error,
) (RecipeElements, error) {
	if vrg.Spec.KubeObjectProtection == nil {
		return RecipeElements{
			PvcSelector: pvcSelectorDefault(vrg),
		}, nil
	}

	if vrg.Spec.KubeObjectProtection.RecipeRef == nil {
		return RecipeElements{
			PvcSelector:     pvcSelectorDefault(vrg),
			CaptureWorkflow: captureWorkflowDefault(vrg),
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

	if err := RecipeParametersExpand(&recipe, vrg.Spec.KubeObjectProtection.RecipeParameters, log); err != nil {
		return RecipeElements{}, errors.Wrap(err, "recipe parameters expand")
	}

	recipeElements := RecipeElements{
		PvcSelector: pvcSelectorRecipeRefNonNil(recipe, vrg),
	}

	return recipeElements, workflowsGet(recipe, &recipeElements, vrg)
}

func recipeWorkflowsGet(recipe recipe.Recipe, recipeElements *RecipeElements, vrg ramen.VolumeReplicationGroup) error {
	var err error

	if recipe.Spec.CaptureWorkflow == nil {
		recipeElements.CaptureWorkflow = captureWorkflowDefault(vrg)
	} else {
		recipeElements.CaptureWorkflow, err = getCaptureGroups(recipe)
		if err != nil {
			return errors.Wrap(err, "Failed to get groups from capture workflow")
		}
	}

	if recipe.Spec.RecoverWorkflow == nil {
		recipeElements.RecoverWorkflow = recoverWorkflowDefault()
	} else {
		recipeElements.RecoverWorkflow, err = getRecoverGroups(recipe)
		if err != nil {
			return errors.Wrap(err, "Failed to get groups from recovery workflow")
		}
	}

	return err
}

func RecipeParametersExpand(recipe *recipe.Recipe, parameters map[string][]string,
	log logr.Logger,
) error {
	spec := &recipe.Spec
	log.Info("Recipe pre-expansion", "spec", *spec, "parameters", parameters)

	bytes, err := json.Marshal(*spec)
	if err != nil {
		return err
	}

	s1 := string(bytes)
	s2 := parametersExpand(s1, parameters)

	if err = json.Unmarshal([]byte(s2), spec); err != nil {
		return err
	}

	log.Info("Recipe post-expansion", "spec", *spec)

	return nil
}

func parametersExpand(s string, parameters map[string][]string) string {
	return os.Expand(s, func(key string) string {
		values := parameters[key]

		return strings.Join(values, `","`)
	})
}
