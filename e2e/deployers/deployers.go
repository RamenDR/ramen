// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"fmt"

	"github.com/ramendr/ramen/e2e/types"
)

// factoryFunc is the new() function type for deployers
type factoryFunc func() types.Deployer

var registry = map[string]factoryFunc{}

// register needs to be called by every deployer in the init() function to
// register itself with the deployer registry.
func register(deployerType string, f factoryFunc) {
	if _, exists := registry[deployerType]; exists {
		panic(fmt.Sprintf("deployer %q already registered", deployerType))
	}

	registry[deployerType] = f
}

// New creates a new deployer for name
func New(name string) (types.Deployer, error) {
	factory := registry[name]
	if factory == nil {
		return nil, fmt.Errorf("unknown deployer %q (choose from %q)", name, AvailableNames())
	}

	return factory(), nil
}

func AvailableNames() []string {
	keys := make([]string, 0, len(registry))

	for k := range registry {
		keys = append(keys, k)
	}

	return keys
}
