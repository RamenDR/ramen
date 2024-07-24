// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/internal/controller/util"
)

func drPolicyPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
	}
}

func drPolicyEventHandler() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(handler.MapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			drpolicy, ok := obj.(*ramen.DRPolicy)
			if !ok {
				return []reconcile.Request{}
			}

			return filterDRClusters(drpolicy)
		}))
}

func filterDRClusters(drpolicy *ramen.DRPolicy) []ctrl.Request {
	requests := []ctrl.Request{}

	for _, cluster := range util.DRPolicyClusterNames(drpolicy) {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: cluster,
			},
		})
	}

	return requests
}
