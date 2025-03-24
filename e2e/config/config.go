// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/viper"

	"github.com/ramendr/ramen/e2e/types"
)

const (
	// Kubernetes distributions
	DistroK8s = "k8s"
	DistroOcp = "ocp"

	// Channel
	defaultChannelNamespace = "e2e-gitops"

	// Git repository
	defaultGitURL    = "https://github.com/RamenDR/ocm-ramen-samples.git"
	defaultGitBranch = "main"

	// DRPolicy
	defaultDRPolicyName = "dr-policy"

	// ClusterSet
	defaultClusterSetName = "default"
)

// Options that can be used in a configuration file.
type Options struct {
	Workloads []string
	Deployers []string
}

// Default namespace mappings for Kubernetes (k8s) clusters.
var k8sNamespaces = types.NamespacesConfig{
	RamenHubNamespace:       "ramen-system",
	RamenDRClusterNamespace: "ramen-system",
	RamenOpsNamespace:       "ramen-ops",
	ArgocdNamespace:         "argocd",
}

// Default namespace mappings for OpenShift (ocp) clusters.
var ocpNamespaces = types.NamespacesConfig{
	RamenHubNamespace:       "openshift-operators",
	RamenDRClusterNamespace: "openshift-dr-system",
	RamenOpsNamespace:       "openshift-dr-ops",
	ArgocdNamespace:         "openshift-gitops",
}

var resourceNameForbiddenCharacters *regexp.Regexp

func ReadConfig(configFile string, options Options) (*types.Config, error) {
	config := &types.Config{}

	if err := readConfig(configFile, config); err != nil {
		return nil, err
	}

	if err := validateDistro(config); err != nil {
		return nil, err
	}

	if err := validateClusters(config); err != nil {
		return nil, err
	}

	if err := validatePVCSpecs(config); err != nil {
		return nil, err
	}

	if err := validateTests(config, &options); err != nil {
		return nil, err
	}

	config.Channel.Name = resourceName(config.Repo.URL)
	config.Channel.Namespace = defaultChannelNamespace

	return config, nil
}

func readConfig(configFile string, config *types.Config) error {
	viper.SetDefault("Repo.URL", defaultGitURL)
	viper.SetDefault("Repo.Branch", defaultGitBranch)
	viper.SetDefault("DRPolicy", defaultDRPolicyName)
	viper.SetDefault("ClusterSet", defaultClusterSetName)

	viper.SetConfigFile(configFile)

	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}

	if err := viper.Unmarshal(config); err != nil {
		return fmt.Errorf("failed to unmarshal config: %v", err)
	}

	return nil
}

func validateDistro(config *types.Config) error {
	switch config.Distro {
	case DistroK8s:
		config.Namespaces = k8sNamespaces
	case DistroOcp:
		config.Namespaces = ocpNamespaces
	default:
		return fmt.Errorf("invalid distro %q: (choose one of %q, %q)",
			config.Distro, DistroK8s, DistroOcp)
	}

	return nil
}

func validateClusters(config *types.Config) error {
	if config.Clusters["hub"].Kubeconfig == "" {
		return fmt.Errorf("failed to find hub cluster in configuration")
	}

	if config.Clusters["c1"].Kubeconfig == "" {
		return fmt.Errorf("failed to find c1 cluster in configuration")
	}

	if config.Clusters["c2"].Kubeconfig == "" {
		return fmt.Errorf("failed to find c2 cluster in configuration")
	}

	return nil
}

func validatePVCSpecs(config *types.Config) error {
	if len(config.PVCSpecs) == 0 {
		return fmt.Errorf("failed to find pvcs in configuration")
	}

	return nil
}

func validateTests(config *types.Config, options *Options) error {
	// We allow an empty test list so one can run the validation tests or unit tests without a fully configured file.
	if len(config.Tests) == 0 {
		return nil
	}

	pvcSpecNames := make([]string, 0, len(config.PVCSpecs))
	for _, spec := range config.PVCSpecs {
		pvcSpecNames = append(pvcSpecNames, spec.Name)
	}

	testsSeen := map[types.TestConfig]struct{}{}

	for _, t := range config.Tests {
		if _, ok := testsSeen[t]; ok {
			return fmt.Errorf("duplicate test (deployer: %q, workload: %q, pvcSpec: %q)",
				t.Deployer, t.Workload, t.PVCSpec)
		}

		if !slices.Contains(options.Deployers, t.Deployer) {
			return fmt.Errorf("invalid test deployer: %q (available %q)", t.Deployer, options.Deployers)
		}

		if !slices.Contains(options.Workloads, t.Workload) {
			return fmt.Errorf("invalid test workload: %q (available %q)", t.Workload, options.Workloads)
		}

		if !slices.Contains(pvcSpecNames, t.PVCSpec) {
			return fmt.Errorf("invalid test pvcSpec: %q (available %q)", t.PVCSpec, pvcSpecNames)
		}

		testsSeen[t] = struct{}{}
	}

	return nil
}

// PVCSpecMap returns a mapping from PVCSpec.Name to PVCSpec.
func PVCSpecsMap(config *types.Config) map[string]types.PVCSpecConfig {
	res := map[string]types.PVCSpecConfig{}
	for _, spec := range config.PVCSpecs {
		res[spec.Name] = spec
	}

	return res
}

// resourceName convert a URL to conventional k8s resource name:
// "https://github.com/foo/bar.git" -> "https-github-com-foo-bar-git"
// https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-subdomain-names
func resourceName(url string) string {
	return strings.ToLower(resourceNameForbiddenCharacters.ReplaceAllString(url, "-"))
}

func init() {
	// Matches one of more forbidden characters, so we can replace them with single replacement character.
	resourceNameForbiddenCharacters = regexp.MustCompile(`[^\w]+`)
}
