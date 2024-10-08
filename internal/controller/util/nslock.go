// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"sync"
)

// NamespaceLock implements atomic operation for namespace. It will have the namespace
// having multiple vrgs in which VRGs are being processed.
type NamespaceLock struct {
	//Namespaces sets.String
	//mux        sync.Mutex
	nslock map[string]*sync.Mutex
}

// NewNamespaceLock returns new NamespaceLock
func NewNamespaceLock() *NamespaceLock {
	// return &NamespaceLock{
	// 	Namespaces: sets.NewString(),
	// }
	return &NamespaceLock{
		nslock: make(map[string]*sync.Mutex),
	}
}

// TryToAcquireLock tries to acquire the lock for processing VRG in a namespace having
// multiple VRGs and returns true if successful.
// If processing has already begun in the namespace, returns false.
func (nl *NamespaceLock) TryToAcquireLock(namespace string) bool {
	// If key is found, return false
	// if key is not found, add key and also lock
	if _, ok := nl.nslock[namespace]; ok {
		if nl.nslock[namespace] != nil {
			return nl.nslock[namespace].TryLock()
		} else {
			// Key exists but not initialized
			nl.nslock[namespace] = new(sync.Mutex)
			nl.nslock[namespace].Lock()
			return true
		}
		// if nl.nslock[namespace] == nil {
		// 	nl.nslock[namespace] = new(sync.Mutex)
		// 	nl.nslock[namespace].Lock()
		// 	return true
		// }
	} else {
		nl.nslock[namespace] = new(sync.Mutex)
		nl.nslock[namespace].Lock()
		return true
	}
	//return false
}

// Release removes lock on the namespace
func (nl *NamespaceLock) Release(namespace string) {
	//nl.mux.Lock()
	//defer nl.mux.Unlock()
	//nl.Namespaces.Delete(namespace)
	//nl.mux.Unlock()
	nl.nslock[namespace].Unlock()
	delete(nl.nslock, namespace)
}
