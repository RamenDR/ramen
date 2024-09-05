// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"sync"
)

// NamespaceLock implements atomic operation for namespace. It will have the namespace
// having multiple vrgs in which VRGs are being processed.
type NamespaceLock struct {
	namespace string
	mux       sync.Mutex
}

// NewNamespaceLock returns new NamespaceLock
func NewNamespaceLock() *NamespaceLock {
	return &NamespaceLock{}
}

// TryToAcquireLock tries to acquire the lock for processing VRG in a namespace having
// multiple VRGs and returns true if successful.
// If processing has already begun in the namespace, returns false.
func (nl *NamespaceLock) TryToAcquireLock(namespace string) bool {
	nl.mux.Lock()
	defer nl.mux.Unlock()

	if nl.namespace == namespace {
		return false
	}

	if nl.namespace == "" {
		nl.namespace = namespace
	}

	return true
}

// Release removes lock on the namespace
func (nl *NamespaceLock) Release(namespace string) {
	nl.mux.Lock()
	defer nl.mux.Unlock()
	nl.namespace = ""
}
