// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"fmt"

	"github.com/ramendr/ramen/e2e/types"
)

type factory func(revision string, pvcSpec types.PVCSpecConfig) types.Workload

var registry = map[string]factory{
	deploymentName: NewDeployment,
}

func New(name, branch string, pvcSpec types.PVCSpecConfig) (types.Workload, error) {
	fac := registry[name]
	if fac == nil {
		return nil, fmt.Errorf("unknown deployment: %q (choose from %q)", name, AvailableNames())
	}

	return fac(branch, pvcSpec), nil
}

func AvailableNames() []string {
	// TODO: Use maps.Keys() when we have Go 1.23.
	keys := make([]string, 0, len(registry))
	for k := range registry {
		keys = append(keys, k)
	}

	return keys
}
