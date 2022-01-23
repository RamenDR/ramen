/*
Copyright 2021 The RamenDR authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	OCMBackupLabelKey   string = "cluster.open-cluster-management.io/backup"
	OCMBackupLabelValue string = "resource"
)

func AddLabel(meta *metav1.ObjectMeta, key, value string) (bool, map[string]string) {
	const labelAdded = true

	labels := meta.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	if _, ok := labels[key]; !ok {
		labels[key] = value

		return labelAdded, labels
	}

	return !labelAdded, nil
}

func AddFinalizer(obj client.Object, finalizer string) bool {
	const finalizerAdded = true

	if !controllerutil.ContainsFinalizer(obj, finalizer) {
		controllerutil.AddFinalizer(obj, finalizer)

		return finalizerAdded
	}

	return !finalizerAdded
}
