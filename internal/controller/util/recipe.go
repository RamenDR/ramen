// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	recipev1 "github.com/ramendr/recipe/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RecipeElements struct {
	PvcSelector         PvcSelector
	CaptureWorkflow     []kubeobjects.CaptureSpec
	RecoverWorkflow     []kubeobjects.RecoverSpec
	CaptureFailOn       string
	RestoreFailOn       string
	RecipeWithParams    *recipev1.Recipe
	StopRecipeReconcile bool
}

type PvcSelector struct {
	LabelSelector  metav1.LabelSelector
	NamespaceNames []string
}
