// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"fmt"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
)

type factory func(revision string, pvcSpec config.PVCSpec) types.Workload

var registry = map[string]factory{
	deploymentName: NewDeployment,
}

func New(name, branch string, pvcSpec config.PVCSpec) (types.Workload, error) {
	fac := registry[name]
	if fac == nil {
		return nil, fmt.Errorf("unknown deployment: %q (choose from %q)", name, AvailableNames())
	}

	return fac(branch, pvcSpec), nil
}

func AvailableNames() []string {
	keys := make([]string, 0, len(registry))
	for k := range registry {
		keys = append(keys, k)
	}

	return keys
}
