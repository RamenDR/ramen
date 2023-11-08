// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func OwnsAcrossNamespaces(
	builder *builder.Builder,
	scheme *runtime.Scheme,
	object client.Object,
	opts ...builder.WatchesOption,
) *builder.Builder {
	groupVersionKinds, _, _ := scheme.ObjectKinds(object) //nolint:errcheck
	log := ctrl.Log.WithValues("gvks", groupVersionKinds)

	return builder.Watches(
		&source.Kind{Type: object},
		handler.EnqueueRequestsFromMapFunc(func(o client.Object) []reconcile.Request {
			labels := o.GetLabels()
			log := log.WithValues(
				"kind", o.GetObjectKind(),
				"name", o.GetNamespace()+"/"+o.GetName(),
				"created", o.GetCreationTimestamp(),
				"gen", o.GetGeneration(),
				"ver", o.GetResourceVersion(),
				"labels", labels,
			)

			if ownerNamespaceName, ownerName, ok := OwnerNamespaceNameAndName(labels); ok {
				log.Info("owner labels found, enqueue owner reconcile")

				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{Namespace: ownerNamespaceName, Name: ownerName}},
				}
			}

			log.Info("owner labels not found")

			return []reconcile.Request{}
		}),
		opts...,
	)
}
