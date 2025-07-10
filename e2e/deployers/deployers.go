// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"fmt"
	"sync"

	"github.com/ramendr/ramen/e2e/types"
)

// factoryFunc is the new() function type for deployers
type factoryFunc func() types.Deployer

var mutex sync.Mutex

var registry map[string]factoryFunc

// register needs to be called by every deployer in the init() function to
// register itself with the deployer registry.
func register(deployerType string, f factoryFunc) {
	mutex.Lock()
	defer mutex.Unlock()

	if registry == nil {
		registry = make(map[string]factoryFunc)
	}

	if _, exists := registry[deployerType]; exists {
		panic(fmt.Sprintf("deployer %q already registered", deployerType))
	}

	registry[deployerType] = f
}

func getFactory(name string) factoryFunc {
	mutex.Lock()
	defer mutex.Unlock()

	return registry[name]
}

// New creates a new deployer for name
func New(name string) (types.Deployer, error) {
	factory := getFactory(name)
	if factory == nil {
		return nil, fmt.Errorf("unknown deployer %q (choose from %q)", name, AvailableNames())
	}

	return factory(), nil
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
