// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type CreateOrDeleteOrResourceVersionUpdatePredicate struct{}

func (CreateOrDeleteOrResourceVersionUpdatePredicate) Create(e event.CreateEvent) bool {
	return true
}

func (CreateOrDeleteOrResourceVersionUpdatePredicate) Delete(e event.DeleteEvent) bool {
	return true
}

func (CreateOrDeleteOrResourceVersionUpdatePredicate) Update(e event.UpdateEvent) bool {
	return predicate.ResourceVersionChangedPredicate{}.Update(e)
}

func (CreateOrDeleteOrResourceVersionUpdatePredicate) Generic(e event.GenericEvent) bool {
	return false
}

type CreateOrResourceVersionUpdatePredicate struct{}

func (CreateOrResourceVersionUpdatePredicate) Create(event.CreateEvent) bool   { return true }
func (CreateOrResourceVersionUpdatePredicate) Delete(event.DeleteEvent) bool   { return false }
func (CreateOrResourceVersionUpdatePredicate) Generic(event.GenericEvent) bool { return false }
func (CreateOrResourceVersionUpdatePredicate) Update(e event.UpdateEvent) bool {
	return predicate.ResourceVersionChangedPredicate{}.Update(e)
}

type ResourceVersionUpdatePredicate struct{}

func (ResourceVersionUpdatePredicate) Create(event.CreateEvent) bool   { return false }
func (ResourceVersionUpdatePredicate) Delete(event.DeleteEvent) bool   { return false }
func (ResourceVersionUpdatePredicate) Generic(event.GenericEvent) bool { return false }
func (ResourceVersionUpdatePredicate) Update(e event.UpdateEvent) bool {
	return predicate.ResourceVersionChangedPredicate{}.Update(e)
}
