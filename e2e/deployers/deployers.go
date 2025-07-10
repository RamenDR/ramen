// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"fmt"
	"maps"
	"slices"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
)

// factoryFunc is the new() function type for deployers
type factoryFunc func(config.Deployer) types.Deployer

var registry = map[string]factoryFunc{}

// New creates a new deployer for name
func New(deployer config.Deployer) (types.Deployer, error) {
	factory := registry[deployer.Type]
	if factory == nil {
		return nil, fmt.Errorf("unknown deployer %q (choose from %q)", deployer.Type, AvailableTypes())
	}

	return factory(deployer), nil
}

func AvailableTypes() []string {
	return slices.Collect(maps.Keys(registry))
}

// register needs to be called by every deployer in the init() function to
// register itself with the deployer registry.
func register(deployerType string, f factoryFunc) {
	if _, exists := registry[deployerType]; exists {
		panic(fmt.Sprintf("deployer %q already registered", deployerType))
	}

	registry[deployerType] = f
}
