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
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/ramendr/ramen/internal/controller/util"
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

const DefaultCaptureRecoverGroupName = "default"

func captureWorkflowDefault(vrg ramen.VolumeReplicationGroup, ramenConfig ramen.RamenConfig) []kubeobjects.CaptureSpec {
	namespaces := []string{vrg.Namespace}

	if vrg.Namespace == RamenOperandsNamespace(ramenConfig) {
		namespaces = *vrg.Spec.ProtectedNamespaces
	}

	captureSpecs := []kubeobjects.CaptureSpec{
		{
			Name: DefaultCaptureRecoverGroupName,
			Spec: kubeobjects.Spec{
				KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
					IncludedNamespaces: namespaces,
				},
			},
		},
	}

	if vrg.Spec.KubeObjectProtection.KubeObjectSelector != nil {
		captureSpecs[0].Spec.LabelSelector = vrg.Spec.KubeObjectProtection.KubeObjectSelector
	}

	return captureSpecs
}

func recoverWorkflowDefault(vrg ramen.VolumeReplicationGroup, ramenConfig ramen.RamenConfig) []kubeobjects.RecoverSpec {
	namespaces := []string{vrg.Namespace}

	if vrg.Namespace == RamenOperandsNamespace(ramenConfig) {
		namespaces = *vrg.Spec.ProtectedNamespaces
	}

	recoverSpecs := []kubeobjects.RecoverSpec{
		{
			BackupName: DefaultCaptureRecoverGroupName,
			Spec: kubeobjects.Spec{
				KubeResourcesSpec: kubeobjects.KubeResourcesSpec{
					IncludedNamespaces: namespaces,
				},
				LabelSelector: vrg.Spec.KubeObjectProtection.KubeObjectSelector,
			},
		},
	}

	return recoverSpecs
}

func GetPVCSelector(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	ramenConfig ramen.RamenConfig,
	log logr.Logger,
) (PvcSelector, error) {
	recipeElements, err := RecipeElementsGet(ctx, reader, vrg, ramenConfig, log)
	if err != nil {
		return PvcSelector{}, err
	}

	return recipeElements.PvcSelector, nil
}

func RecipeElementsGet(ctx context.Context, reader client.Reader, vrg ramen.VolumeReplicationGroup,
	ramenConfig ramen.RamenConfig, log logr.Logger,
) (RecipeElements, error) {
	var recipeElements RecipeElements

	if vrg.Spec.KubeObjectProtection == nil {
		recipeElements = RecipeElements{
			PvcSelector: getPVCSelector(vrg, ramenConfig, nil, nil),
		}

		return recipeElements, nil
	}

	if vrg.Spec.KubeObjectProtection.RecipeRef == nil {
		recipeElements = RecipeElements{
			PvcSelector:     getPVCSelector(vrg, ramenConfig, nil, nil),
			CaptureWorkflow: captureWorkflowDefault(vrg, ramenConfig),
			RecoverWorkflow: recoverWorkflowDefault(vrg, ramenConfig),
		}

		return recipeElements, nil
	}

	recipeNamespacedName := types.NamespacedName{
		Namespace: vrg.Spec.KubeObjectProtection.RecipeRef.Namespace,
		Name:      vrg.Spec.KubeObjectProtection.RecipeRef.Name,
	}

	recipe := recipe.Recipe{}
	if err := reader.Get(ctx, recipeNamespacedName, &recipe); err != nil {
		return recipeElements, fmt.Errorf("recipe %v get error: %w", recipeNamespacedName.String(), err)
	}

	if err := RecipeParametersExpand(&recipe, vrg.Spec.KubeObjectProtection.RecipeParameters, log); err != nil {
		return recipeElements, fmt.Errorf("recipe %v parameters expansion error: %w", recipeNamespacedName.String(), err)
	}

	var selector PvcSelector
	if recipe.Spec.Volumes == nil {
		selector = getPVCSelector(vrg, ramenConfig, nil, nil)
	} else {
		selector = getPVCSelector(vrg, ramenConfig, recipe.Spec.Volumes.IncludedNamespaces,
			recipe.Spec.Volumes.LabelSelector)
	}

	recipeElements = RecipeElements{
		PvcSelector: selector,
	}

	if err := recipeWorkflowsGet(recipe, &recipeElements, vrg, ramenConfig); err != nil {
		return recipeElements, fmt.Errorf("recipe %v workflows get error: %w", recipeNamespacedName.String(), err)
	}

	if err := recipeNamespacesValidate(recipeElements, vrg, ramenConfig); err != nil {
		return recipeElements, fmt.Errorf("recipe %v namespaces validation error: %w", recipeNamespacedName.String(), err)
	}

	return recipeElements, nil
}

func RecipeParametersExpand(recipe *recipe.Recipe, parameters map[string][]string,
	log logr.Logger,
) error {
	spec := &recipe.Spec
	log.V(1).Info("Recipe pre-expansion", "spec", *spec, "parameters", parameters)

	bytes, err := json.Marshal(*spec)
	if err != nil {
		return fmt.Errorf("recipe %s json marshal error: %w", recipe.GetName(), err)
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

func recipeWorkflowsGet(recipe recipe.Recipe, recipeElements *RecipeElements, vrg ramen.VolumeReplicationGroup,
	ramenConfig ramen.RamenConfig,
) error {
	var err error

	recipeElements.CaptureWorkflow, err = getCaptureGroups(recipe)
	if err != nil && err != ErrWorkflowNotFound {
		return fmt.Errorf("failed to get groups from capture workflow: %w", err)
	}

	if err != nil {
		recipeElements.CaptureWorkflow = captureWorkflowDefault(vrg, ramenConfig)
	}

	recipeElements.RecoverWorkflow, err = getRecoverGroups(recipe)
	if err != nil && err != ErrWorkflowNotFound {
		return fmt.Errorf("failed to get groups from recovery workflow: %w", err)
	}

	if err != nil {
		recipeElements.RecoverWorkflow = recoverWorkflowDefault(vrg, ramenConfig)
	}

	return nil
}

func recipeNamespacesValidate(recipeElements RecipeElements, vrg ramen.VolumeReplicationGroup,
	ramenConfig ramen.RamenConfig,
) error {
	extraVrgNamespaceNames := sets.List(recipeNamespaceNames(recipeElements).Delete(vrg.Namespace))

	if len(extraVrgNamespaceNames) == 0 {
		return nil
	}

	if !ramenConfig.MultiNamespace.FeatureEnabled {
		return fmt.Errorf("requested protection of other namespaces when MultiNamespace feature is disabled. %v: %v",
			"other namespaces", extraVrgNamespaceNames)
	}

	if !vrgInAdminNamespace(&vrg, &ramenConfig) {
		vrgAdminNamespaceNames := vrgAdminNamespaceNames(ramenConfig)

		return fmt.Errorf("vrg namespace: %v needs to be in admin namespaces: %v to protect other namespaces: %v",
			vrg.Namespace,
			vrgAdminNamespaceNames,
			extraVrgNamespaceNames,
		)
	}

	// we know vrg is in one of the admin namespaces but if the vrg is in the ramen ops namespace
	// then the every namespace in recipe should be in the protected namespace list.
	if vrg.Namespace == RamenOperandsNamespace(ramenConfig) {
		for _, ns := range extraVrgNamespaceNames {
			if !slices.Contains(*vrg.Spec.ProtectedNamespaces, ns) {
				return fmt.Errorf("recipe mentions namespace: %v which is not in protected namespaces: %v",
					ns,
					vrg.Spec.ProtectedNamespaces,
				)
			}
		}
	}

	// vrg is in the ramen operator namespace, allow it to protect any namespace
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
