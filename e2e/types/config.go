// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"maps"
	"slices"
)

// Equal return true if config is equal to other config.
//
//nolint:cyclop
func (c *Config) Equal(o *Config) bool {
	if c == o {
		return true
	}

	if c.Distro != o.Distro {
		return false
	}

	if c.Repo != o.Repo {
		return false
	}

	if c.DRPolicy != o.DRPolicy {
		return false
	}

	if c.ClusterSet != o.ClusterSet {
		return false
	}

	if !maps.Equal(c.Clusters, o.Clusters) {
		return false
	}

	if !slices.Equal(c.PVCSpecs, o.PVCSpecs) {
		return false
	}

	if !slices.Equal(c.Tests, o.Tests) {
		return false
	}

	if c.Channel != o.Channel {
		return false
	}

	if c.Namespaces != o.Namespaces {
		return false
	}

	return true
}
