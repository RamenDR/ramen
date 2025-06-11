// Disabling testpackage linter to test unexported functions in the config package.
//
//nolint:testpackage
package config

import (
	"testing"
)

func TestReadConfig(t *testing.T) {
	options := Options{
		Deployers: []string{"appset", "subscr", "disapp"},
		Workloads: []string{"deploy"},
	}

	c, err := ReadConfig("testdata/test.yaml", options)
	if err != nil {
		t.Fatal(err)
	}

	expected := &Config{
		Clusters: map[string]Cluster{
			"hub": {Kubeconfig: "hub/config"},
			"c1":  {Kubeconfig: "dr1/config"},
			"c2":  {Kubeconfig: "dr2/config"},
		},
		ClusterSet: "default",
		Repo: Repo{
			URL:    "https://github.com/RamenDR/ocm-ramen-samples.git",
			Branch: "main",
		},
		DRPolicy: "dr-policy",
		PVCSpecs: []PVCSpec{
			{Name: "rbd", StorageClassName: "rook-ceph-block", AccessModes: "ReadWriteOnce"},
			{Name: "cephfs", StorageClassName: "rook-cephfs-fs1", AccessModes: "ReadWriteMany"},
		},
		Tests: []Test{
			{Workload: "deploy", Deployer: "appset", PVCSpec: "rbd"},
			{Workload: "deploy", Deployer: "appset", PVCSpec: "cephfs"},
		},
		Channel: Channel{
			Name:      "https-github-com-ramendr-ocm-ramen-samples-git",
			Namespace: "e2e-gitops",
		},
	}
	if !c.Equal(expected) {
		t.Fatalf("expected %+v, got %+v", expected, c)
	}
}

func TestValidatePVCSpecs(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
		valid  bool
	}{
		{
			name: "valid",
			config: &Config{
				PVCSpecs: []PVCSpec{
					{Name: "rbd", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
					{Name: "fs", StorageClassName: "filesystem", AccessModes: "ReadWriteMany"},
				},
			},
			valid: true,
		},
		{
			name: "empty",
			config: &Config{
				PVCSpecs: []PVCSpec{},
			},
			valid: false,
		},
		{
			name: "duplicate names",
			config: &Config{
				PVCSpecs: []PVCSpec{
					{Name: "rbd", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
					{Name: "rbd", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
				},
			},
			valid: false,
		},
		{
			name: "duplicate content",
			config: &Config{
				PVCSpecs: []PVCSpec{
					{Name: "rbd", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
					{Name: "cephfs", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
				},
			},
			valid: false,
		},
		{
			name: "invalid storage class name",
			config: &Config{
				PVCSpecs: []PVCSpec{
					{Name: "rbd", StorageClassName: "not a dns subdomain name", AccessModes: "ReadWriteOnce"},
				},
			},
			valid: false,
		},
		{
			name: "invalid access mode",
			config: &Config{
				PVCSpecs: []PVCSpec{
					{Name: "rbd", StorageClassName: "standard", AccessModes: "InvalidAccessMode"},
				},
			},
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePVCSpecs(tt.config)
			if tt.valid && err != nil {
				t.Errorf("valid config %+v, failed: %s", tt.config.PVCSpecs, err)
			}

			if !tt.valid && err == nil {
				t.Errorf("invalid config %+v, did not fail", tt.config.PVCSpecs)
			}
		})
	}
}

func baseConfig() *Config {
	return &Config{
		Clusters: map[string]Cluster{
			"hub": {Kubeconfig: "hub-kubeconfig"},
			"c1":  {Kubeconfig: "c1-kubeconfig"},
			"c2":  {Kubeconfig: "c2-kubeconfig"},
		},
		ClusterSet: "clusterset",
		Distro:     "ocp",
		Namespaces: Namespaces{
			RamenHubNamespace:       "ramen-hub",
			RamenDRClusterNamespace: "ramen-dr-cluster",
			RamenOpsNamespace:       "ramen-ops",
			ArgocdNamespace:         "argocd",
		},
		Repo:     Repo{URL: "https://github.com/org/repo", Branch: "main"},
		DRPolicy: "dr-policy",
		PVCSpecs: []PVCSpec{
			{Name: "pvc-a", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
		},
		Tests: []Test{
			{Workload: "wl1", Deployer: "ocm-hub", PVCSpec: "pvc-a"},
		},
		Channel: Channel{
			Name:      "my-channel",
			Namespace: "ramen-system",
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
		Modify func(c *Config)
	}

	tests := []test{
		{
			Name: "empty config",
			Modify: func(c *Config) {
				*c = Config{}
			},
		},
		{
			Name:   "different distro",
			Modify: func(c *Config) { c.Distro = "modified-distro" },
		},
		{
			Name: "different repo url",
			Modify: func(c *Config) {
				c.Repo.URL = "https://github.com/org/modified"
			},
		},
		{
			Name: "different drpolicy",
			Modify: func(c *Config) {
				c.DRPolicy = "modified-policy"
			},
		},
		{
			Name: "different clusterSet",
			Modify: func(c *Config) {
				c.ClusterSet = "modified-clusterset"
			},
		},
		{
			Name: "different cluster kubeconfig",
			Modify: func(c *Config) {
				c.Clusters["c1"] = Cluster{Kubeconfig: "modified-kubeconfig"}
			},
		},
		{
			Name: "different pvcspec",
			Modify: func(c *Config) {
				c.PVCSpecs[0].StorageClassName = "modified-sc"
			},
		},
		{
			Name: "different test workload",
			Modify: func(c *Config) {
				c.Tests[0].Workload = "modified-workload"
			},
		},
		{
			Name: "different channel name",
			Modify: func(c *Config) {
				c.Channel.Name = "modified-channel"
			},
		},
		{
			Name: "different ramen hub namespace",
			Modify: func(c *Config) {
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
