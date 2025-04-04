// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"fmt"

	"github.com/ramendr/ramen/e2e/types"
)

var registry map[string]types.Deployer

func init() {
	appset := &ApplicationSet{}
	subscr := &Subscription{}
	disapp := &DiscoveredApp{}

	registry = map[string]types.Deployer{
		appset.GetName(): appset,
		subscr.GetName(): subscr,
		disapp.GetName(): disapp,
	}
}

// New creates a new deployer for name
func New(name string) (types.Deployer, error) {
	deployer := registry[name]
	if deployer == nil {
		return nil, fmt.Errorf("unknown deployer %q (choose from %q)", name, AvailableNames())
	}

	return deployer, nil
}

func AvailableNames() []string {
	keys := make([]string, 0, len(registry))
	for k := range registry {
		keys = append(keys, k)
	}

	return keys
}
