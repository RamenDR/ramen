// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/viper"
)

const (
	defaultChannelNamespace = "e2e-gitops"
	defaultGitURL           = "https://github.com/RamenDR/ocm-ramen-samples.git"
)

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

type Config struct {
	// User configurable values.
	ChannelNamespace string
	GitURL           string
	Clusters         map[string]Cluster
	PVCSpecs         []PVCSpec

	// Generated values
	channelName string
}

var (
	resourceNameForbiddenCharacters *regexp.Regexp
	config                          = &Config{}
)

//nolint:cyclop
func ReadConfig(configFile string) error {
	viper.SetDefault("ChannelNamespace", defaultChannelNamespace)
	viper.SetDefault("GitURL", defaultGitURL)

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

	config.channelName = resourceName(config.GitURL)

	return nil
}

func GetChannelName() string {
	return config.channelName
}

func GetChannelNamespace() string {
	return config.ChannelNamespace
}

func GetGitURL() string {
	return config.GitURL
}

func GetPVCSpecs() []PVCSpec {
	return config.PVCSpecs
}

func GetClusters() map[string]Cluster {
	return config.Clusters
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
