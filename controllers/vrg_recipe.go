// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/go-logr/logr"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/kubeobjects"
	"github.com/ramendr/ramen/controllers/util"
	recipe "github.com/ramendr/recipe/api/v1alpha1"
	"golang.org/x/exp/slices"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
	ramenConfig ramen.RamenConfig,
	log logr.Logger,
) (PvcSelector, error) {
	var recipeElements RecipeElements

	return recipeElements.PvcSelector, recipeVolumesAndOptionallyWorkflowsGet(
		ctx, reader, vrg, ramenConfig, log, &recipeElements,
		func(recipe.Recipe, *RecipeElements, ramen.VolumeReplicationGroup) error { return nil },
	)
}

func RecipeElementsGet(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	ramenConfig ramen.RamenConfig, log logr.Logger, recipeElements *RecipeElements,
) error {
	return recipeVolumesAndOptionallyWorkflowsGet(ctx, reader, vrg, ramenConfig, log, recipeElements,
		recipeWorkflowsGet,
	)
}

func recipeVolumesAndOptionallyWorkflowsGet(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	ramenConfig ramen.RamenConfig, log logr.Logger, recipeElements *RecipeElements,
	workflowsGet func(recipe.Recipe, *RecipeElements, ramen.VolumeReplicationGroup) error,
) error {
	if vrg.Spec.KubeObjectProtection == nil {
		*recipeElements = RecipeElements{
			PvcSelector: pvcSelectorDefault(vrg),
		}

		return nil
	}

	if vrg.Spec.KubeObjectProtection.RecipeRef == nil {
		*recipeElements = RecipeElements{
			PvcSelector:     pvcSelectorDefault(vrg),
			CaptureWorkflow: captureWorkflowDefault(vrg),
			RecoverWorkflow: recoverWorkflowDefault(),
		}

		return nil
	}

	recipeNamespacedName := types.NamespacedName{
		Namespace: vrg.Spec.KubeObjectProtection.RecipeRef.Namespace,
		Name:      vrg.Spec.KubeObjectProtection.RecipeRef.Name,
	}

	recipe := recipe.Recipe{}
	if err := reader.Get(ctx, recipeNamespacedName, &recipe); err != nil {
		return fmt.Errorf("recipe %v get error: %w", recipeNamespacedName.String(), err)
	}

	if err := RecipeParametersExpand(&recipe, vrg.Spec.KubeObjectProtection.RecipeParameters, log); err != nil {
		return err
	}

	*recipeElements = RecipeElements{
		PvcSelector: pvcSelectorRecipeRefNonNil(recipe, vrg),
	}

	if err := workflowsGet(recipe, recipeElements, vrg); err != nil {
		return err
	}

	return recipeNamespacesValidate(*recipeElements, vrg, ramenConfig)
}

func RecipeParametersExpand(recipe *recipe.Recipe, parameters map[string][]string,
	log logr.Logger,
) error {
	spec := &recipe.Spec
	log.V(1).Info("Recipe pre-expansion", "spec", *spec, "parameters", parameters)

	bytes, err := json.Marshal(*spec)
	if err != nil {
		return fmt.Errorf("recipe spec %+v json marshal error: %w", *spec, err)
	}

	s1 := string(bytes)
	s2 := parametersExpand(s1, parameters)

	if err = json.Unmarshal([]byte(s2), spec); err != nil {
		return fmt.Errorf("recipe spec %v json unmarshal error: %w", s2, err)
	}

	log.V(1).Info("Recipe post-expansion", "spec", *spec)

	return nil
}

func parametersExpand(s string, parameters map[string][]string) string {
	return os.Expand(s, func(key string) string {
		values := parameters[key]

		return strings.Join(values, `","`)
	})
}

func recipeWorkflowsGet(recipe recipe.Recipe, recipeElements *RecipeElements, vrg ramen.VolumeReplicationGroup) error {
	var err error

	if recipe.Spec.CaptureWorkflow == nil {
		recipeElements.CaptureWorkflow = captureWorkflowDefault(vrg)
	} else {
		recipeElements.CaptureWorkflow, err = getCaptureGroups(recipe)
		if err != nil {
			return fmt.Errorf("failed to get groups from capture workflow: %w", err)
		}
	}

	if recipe.Spec.RecoverWorkflow == nil {
		recipeElements.RecoverWorkflow = recoverWorkflowDefault()
	} else {
		recipeElements.RecoverWorkflow, err = getRecoverGroups(recipe)
		if err != nil {
			return fmt.Errorf("failed to get groups from recovery workflow: %w", err)
		}
	}

	return err
}

func recipeNamespacesValidate(recipeElements RecipeElements, vrg ramen.VolumeReplicationGroup,
	ramenConfig ramen.RamenConfig,
) error {
	extraVrgNamespaceNames := sets.List(recipeNamespaceNames(recipeElements).Delete(vrg.Namespace))

	if len(extraVrgNamespaceNames) == 0 {
		return nil
	}

	if !ramenConfig.MultiNamespace.FeatureEnabled {
		return fmt.Errorf("extra-VRG namespaces %v require feature be enabled", extraVrgNamespaceNames)
	}

	adminNamespaceNames := adminNamespaceNames()
	if !slices.Contains(adminNamespaceNames, vrg.Namespace) {
		return fmt.Errorf("extra-VRG namespaces %v require VRG's namespace, %v, be an admin one %v",
			extraVrgNamespaceNames,
			vrg.Namespace,
			adminNamespaceNames,
		)
	}

	if vrg.Spec.Async != nil {
		return fmt.Errorf("extra-VRG namespaces %v require VRG's async mode be disabled", extraVrgNamespaceNames)
	}

	return nil
}

func recipeNamespaceNames(recipeElements RecipeElements) sets.Set[string] {
	namespaceNames := make(sets.Set[string], 0)

	namespaceNames.Insert(recipeElements.PvcSelector.NamespaceNames...)

	for _, captureSpec := range recipeElements.CaptureWorkflow {
		namespaceNames.Insert(captureSpec.IncludedNamespaces...)
	}

	for _, recoverSpec := range recipeElements.RecoverWorkflow {
		namespaceNames.Insert(recoverSpec.IncludedNamespaces...)
	}

	return namespaceNames
}

func recipesWatch(b *builder.Builder, m objectToReconcileRequestsMapper) *builder.Builder {
	return b.Watches(
		&recipe.Recipe{},
		handler.EnqueueRequestsFromMapFunc(m.recipeToVrgReconcileRequestsMapper),
		builder.WithPredicates(util.CreateOrResourceVersionUpdatePredicate{}),
	)
}

func (m objectToReconcileRequestsMapper) recipeToVrgReconcileRequestsMapper(
	ctx context.Context,
	recipe client.Object,
) []reconcile.Request {
	recipeNamespacedName := types.NamespacedName{
		Namespace: recipe.GetNamespace(),
		Name:      recipe.GetName(),
	}
	log := m.log.WithName("recipe").WithName("VolumeReplicationGroup").WithValues(
		"name", recipeNamespacedName.String(),
		"creation", recipe.GetCreationTimestamp(),
		"uid", recipe.GetUID(),
		"generation", recipe.GetGeneration(),
		"version", recipe.GetResourceVersion(),
	)

	vrgList := ramen.VolumeReplicationGroupList{}
	if err := m.reader.List(context.TODO(), &vrgList); err != nil {
		log.Error(err, "vrg list retrieval error")

		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, 0, len(vrgList.Items))

	for _, vrg := range vrgList.Items {
		if vrg.Spec.KubeObjectProtection == nil ||
			vrg.Spec.KubeObjectProtection.RecipeRef == nil ||
			vrg.Spec.KubeObjectProtection.RecipeRef.Namespace != recipe.GetNamespace() ||
			vrg.Spec.KubeObjectProtection.RecipeRef.Name != recipe.GetName() {
			continue
		}

		vrgNamespacedName := types.NamespacedName{Namespace: vrg.Namespace, Name: vrg.Name}

		requests = append(requests, reconcile.Request{NamespacedName: vrgNamespacedName})

		log.Info("Request VRG reconcile", "VRG", vrgNamespacedName.String())
	}

	return requests
}
