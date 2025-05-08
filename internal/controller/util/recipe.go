// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RecipeElements struct {
	PvcSelector     PvcSelector
	CaptureWorkflow []kubeobjects.CaptureSpec
	RecoverWorkflow []kubeobjects.RecoverSpec
	CaptureFailOn   string
	RestoreFailOn   string
}

type PvcSelector struct {
	LabelSelector  metav1.LabelSelector
	NamespaceNames []string
}
