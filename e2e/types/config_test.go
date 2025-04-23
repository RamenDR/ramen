// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package types_test

import (
	"testing"

	"github.com/ramendr/ramen/e2e/types"
)

func baseConfig() *types.Config {
	return &types.Config{
		Distro:     "ocp",
		Repo:       types.RepoConfig{URL: "https://github.com/org/repo", Branch: "main"},
		DRPolicy:   "dr-policy",
		ClusterSet: "clusterset",
		Clusters: map[string]types.ClusterConfig{
			"hub": {Kubeconfig: "hub-kubeconfig"},
			"c1":  {Kubeconfig: "c1-kubeconfig"},
			"c2":  {Kubeconfig: "c2-kubeconfig"},
		},
		PVCSpecs: []types.PVCSpecConfig{
			{Name: "pvc-a", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
		},
		Tests: []types.TestConfig{
			{Workload: "wl1", Deployer: "ocm-hub", PVCSpec: "pvc-a"},
		},
		Channel: types.ChannelConfig{
			Name:      "my-channel",
			Namespace: "ramen-system",
		},
		Namespaces: types.NamespacesConfig{
			RamenHubNamespace:       "ramen-hub",
			RamenDRClusterNamespace: "ramen-dr-cluster",
			RamenOpsNamespace:       "ramen-ops",
			ArgocdNamespace:         "argocd",
		},
	}
}

func TestConfigEqual(t *testing.T) {
	c1 := baseConfig()
	c2 := baseConfig()

	// intentionally comparing config to itself
	// nolint:gocritic
	t.Run("equal to itself", func(t *testing.T) {
		if !c1.Equal(c1) {
			t.Errorf("config %+v is not equal to itself", c1)
		}
	})

	t.Run("equal to other identical config", func(t *testing.T) {
		if !c1.Equal(c2) {
			t.Errorf("config %+v is not equal to other identical config %+v", c1, c2)
		}
	})
}

func TestConfigNotEqual(t *testing.T) {
	type test struct {
		Name   string
		Modify func(c *types.Config)
	}

	tests := []test{
		{
			Name: "empty config",
			Modify: func(c *types.Config) {
				*c = types.Config{}
			},
		},
		{
			Name:   "different distro",
			Modify: func(c *types.Config) { c.Distro = "modified-distro" },
		},
		{
			Name: "different repo url",
			Modify: func(c *types.Config) {
				c.Repo.URL = "https://github.com/org/modified"
			},
		},
		{
			Name: "different drpolicy",
			Modify: func(c *types.Config) {
				c.DRPolicy = "modified-policy"
			},
		},
		{
			Name: "different clusterSet",
			Modify: func(c *types.Config) {
				c.ClusterSet = "modified-clusterset"
			},
		},
		{
			Name: "different cluster kubeconfig",
			Modify: func(c *types.Config) {
				c.Clusters["c1"] = types.ClusterConfig{Kubeconfig: "modified-kubeconfig"}
			},
		},
		{
			Name: "different pvcspec",
			Modify: func(c *types.Config) {
				c.PVCSpecs[0].StorageClassName = "modified-sc"
			},
		},
		{
			Name: "different test workload",
			Modify: func(c *types.Config) {
				c.Tests[0].Workload = "modified-workload"
			},
		},
		{
			Name: "different channel name",
			Modify: func(c *types.Config) {
				c.Channel.Name = "modified-channel"
			},
		},
		{
			Name: "different ramen hub namespace",
			Modify: func(c *types.Config) {
				c.Namespaces.RamenHubNamespace = "modified-ns"
			},
		},
	}

	c1 := baseConfig()

	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			c2 := baseConfig()
			tt.Modify(c2)

			if c1.Equal(c2) {
				t.Errorf("config %+v is equal to non-equal config %+v", c1, c2)
			}
		})
	}
}
