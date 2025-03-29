// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	CreatedByRamenLabel = "ramendr.openshift.io/resource-created-by-ramen"
)

func ObjectCreatedByRamenSetLabel(object client.Object) {
	labels := object.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}

	labels[CreatedByRamenLabel] = "true"
	object.SetLabels(labels)
}
