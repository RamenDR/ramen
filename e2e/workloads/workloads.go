// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"fmt"
	"maps"
	"slices"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
)

// factoryFunc is the new() function type for workloads
type factoryFunc func(repo config.Repo, pvcSpec config.PVCSpec) (types.Workload, error)

var registry = map[string]factoryFunc{}

func New(name string, repo config.Repo, pvcSpec config.PVCSpec) (types.Workload, error) {
	factory := registry[name]
	if factory == nil {
		return nil, fmt.Errorf("unknown workload: %q (choose from %q)", name, AvailableNames())
	}

	return factory(repo, pvcSpec)
}

func AvailableNames() []string {
	return slices.Collect(maps.Keys(registry))
}

// register needs to be called by every workload in the init() function to
// register itself with the workload registry.
func register(workloadType string, f factoryFunc) {
	if _, exists := registry[workloadType]; exists {
		panic(fmt.Sprintf("workload %q already registered", workloadType))
	}

	registry[workloadType] = f
}
