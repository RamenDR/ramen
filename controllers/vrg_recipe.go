// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"

	"github.com/go-logr/logr"
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

type foo func(recipe.Recipe, *recipeElements, logr.Logger) error

func GetPVCSelector(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	log logr.Logger,
) (PvcSelector, error) {
	recipeElements, err := recipeElementsGetFoo(ctx, reader, vrg, log,
		func(recipe.Recipe, *recipeElements, logr.Logger) error { return nil },
	)

	return recipeElements.pvcSelector, err
}

func recipeElementsGet(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	log logr.Logger,
) (recipeElements, error) {
	return recipeElementsGetFoo(ctx, reader, vrg, log, recipeWorkflowsGet)
}

func recipeElementsGetFoo(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	log logr.Logger, foo foo,
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
		log.Error(err, "recipe get")

		return recipeElements{}, err
	}

	recipeElements := recipeElements{
		pvcSelector: pvcSelectorRecipeRefNonNil(recipe, vrg),
	}

	return recipeElements, foo(recipe, &recipeElements, log)
}

func recipeWorkflowsGet(recipe recipe.Recipe, recipeElements *recipeElements, log logr.Logger) error {
	var err error

	recipeElements.captureWorkflow, err = getCaptureGroups(recipe)
	if err != nil {
		log.Error(err, "Failed to get groups from capture workflow")

		return err
	}

	log.Info("Successfully found recipe capture groups")

	recipeElements.recoverWorkflow, err = getRecoverGroups(recipe)
	if err != nil {
		log.Error(err, "Failed to get groups from recovery workflow")

		return err
	}

	log.Info("Successfully found recipe recovery groups")

	return err
}
