// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/viper"
)

const (
	defaultChannelNamespace = "e2e-gitops"
	defaultGitURL           = "https://github.com/RamenDR/ocm-ramen-samples.git"
	defaultGitBranch        = "main"
	defaultDRPolicyName     = "dr-policy"
)

// Channel defines the name and namespace for the channel CR.
// This is not user-configurable and always uses default values.
type Channel struct {
	Name      string
	Namespace string
}

// Repo represents the user-configurable git repository settings.
// It includes the repository url and branch to be used for deploying workload.
type Repo struct {
	URL    string
	Branch string
}

type PVCSpec struct {
	Name                 string
	StorageClassName     string
	AccessModes          string
	UnsupportedDeployers []string
}

type Cluster struct {
	Name           string
	KubeconfigPath string
}

type Test struct {
	Workload string
	Deployer string
	PVCSpec  string
}

type Config struct {
	// User configurable values.
	Repo     Repo
	DRPolicy string
	Clusters map[string]Cluster
	PVCSpecs []PVCSpec
	Tests    []Test

	// Generated values
	Channel Channel
}

// Options that can be used in a configuration file.
type Options struct {
	Workloads []string
	Deployers []string
}

var (
	resourceNameForbiddenCharacters *regexp.Regexp
	config                          = &Config{}
)

//nolint:cyclop
func ReadConfig(configFile string, options Options) error {
	viper.SetDefault("Repo.URL", defaultGitURL)
	viper.SetDefault("Repo.Branch", defaultGitBranch)
	viper.SetDefault("DRPolicy", defaultDRPolicyName)

	viper.SetConfigFile(configFile)

	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}

	if err := viper.Unmarshal(config); err != nil {
		return fmt.Errorf("failed to unmarshal config: %v", err)
	}

	if config.Clusters["hub"].KubeconfigPath == "" {
		return fmt.Errorf("failed to find hub cluster in configuration")
	}

	if config.Clusters["c1"].KubeconfigPath == "" {
		return fmt.Errorf("failed to find c1 cluster in configuration")
	}

	if config.Clusters["c2"].KubeconfigPath == "" {
		return fmt.Errorf("failed to find c2 cluster in configuration")
	}

	if len(config.PVCSpecs) == 0 {
		return fmt.Errorf("failed to find pvcs in configuration")
	}

	if err := validateTests(config, &options); err != nil {
		return err
	}

	config.Channel.Name = resourceName(config.Repo.URL)
	config.Channel.Namespace = defaultChannelNamespace

	return nil
}

func validateTests(config *Config, options *Options) error {
	if len(config.Tests) == 0 {
		return fmt.Errorf("no tests found")
	}

	pvcSpecNames := make([]string, 0, len(config.PVCSpecs))
	for _, spec := range config.PVCSpecs {
		pvcSpecNames = append(pvcSpecNames, spec.Name)
	}

	testsSeen := map[Test]struct{}{}

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

func GetChannelName() string {
	return config.Channel.Name
}

func GetChannelNamespace() string {
	return config.Channel.Namespace
}

func GetGitURL() string {
	return config.Repo.URL
}

func GetGitBranch() string {
	return config.Repo.Branch
}

func GetDRPolicyName() string {
	return config.DRPolicy
}

func GetPVCSpecs() map[string]PVCSpec {
	res := map[string]PVCSpec{}
	for _, spec := range config.PVCSpecs {
		res[spec.Name] = spec
	}

	return res
}

func GetClusters() map[string]Cluster {
	return config.Clusters
}

func GetTests() []Test {
	return config.Tests
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
