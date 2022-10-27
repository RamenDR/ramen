// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type createOrDeleteOrResourceVersionUpdatePredicate struct{}

func (createOrDeleteOrResourceVersionUpdatePredicate) Create(e event.CreateEvent) bool {
	return true
}

func (createOrDeleteOrResourceVersionUpdatePredicate) Delete(e event.DeleteEvent) bool {
	return true
}

func (createOrDeleteOrResourceVersionUpdatePredicate) Update(e event.UpdateEvent) bool {
	return predicate.ResourceVersionChangedPredicate{}.Update(e)
}

func (createOrDeleteOrResourceVersionUpdatePredicate) Generic(e event.GenericEvent) bool {
	return false
}

type ResourceVersionUpdatePredicate struct{}

func (ResourceVersionUpdatePredicate) Create(event.CreateEvent) bool   { return false }
func (ResourceVersionUpdatePredicate) Delete(event.DeleteEvent) bool   { return false }
func (ResourceVersionUpdatePredicate) Generic(event.GenericEvent) bool { return false }
func (ResourceVersionUpdatePredicate) Update(e event.UpdateEvent) bool {
	return predicate.ResourceVersionChangedPredicate{}.Update(e)
}
