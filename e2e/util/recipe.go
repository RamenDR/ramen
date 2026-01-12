// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"fmt"

	recipe "github.com/ramendr/recipe/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ramendr/ramen/e2e/types"
)

func CreateRecipeOnManagedClusters(ctx types.TestContext, recipe *recipe.Recipe) error {
	env := ctx.Env()

	for _, cluster := range env.ManagedClusters() {
		recipeCopy := recipe.DeepCopy()

		err := cluster.Client.Create(ctx.Context(), recipeCopy)
		if err != nil {
			return fmt.Errorf("failed to create Recipe %q in cluster %q: %w",
				recipe.Name, cluster.Name, err)
		}
	}

	return nil
}

func DeleteRecipeOnManagedClusters(ctx types.TestContext, recipeName string) error {
	env := ctx.Env()

	for _, cluster := range env.ManagedClusters() {
		recipe := &recipe.Recipe{
			ObjectMeta: v1.ObjectMeta{
				Name:      ctx.Name(),
				Namespace: ctx.AppNamespace(),
			},
		}

		err := cluster.Client.Delete(ctx.Context(), recipe)
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}

			return fmt.Errorf("failed to delete Recipe %q in cluster %q: %w",
				recipeName, cluster.Name, err)
		}
	}

	return nil
}
