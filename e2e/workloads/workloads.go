// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"fmt"
	"sync"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
)

// factoryFunc is the new() function type for workloads
type factoryFunc func(revision string, pvcSpec config.PVCSpec) types.Workload

var mutex sync.Mutex

var registry map[string]factoryFunc

// register needs to be called by every workload in the init() function to
// register itself with the workload registry.
func register(workloadType string, f factoryFunc) {
	mutex.Lock()
	defer mutex.Unlock()

	if registry == nil {
		registry = make(map[string]factoryFunc)
	}

	if _, exists := registry[workloadType]; exists {
		panic(fmt.Sprintf("workload %q already registered", workloadType))
	}

	registry[workloadType] = f
}

func getFactory(name string) factoryFunc {
	mutex.Lock()
	defer mutex.Unlock()

	return registry[name]
}

func New(name, branch string, pvcSpec config.PVCSpec) (types.Workload, error) {
	fac := getFactory(name)
	if fac == nil {
		return nil, fmt.Errorf("unknown deployment: %q (choose from %q)", name, AvailableNames())
	}

	return fac(branch, pvcSpec), nil
}

func AvailableNames() []string {
	mutex.Lock()
	defer mutex.Unlock()

	keys := make([]string, 0, len(registry))

	for k := range registry {
		keys = append(keys, k)
	}

	return keys
}
