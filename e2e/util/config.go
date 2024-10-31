// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"fmt"

	"github.com/go-logr/logr"
	"github.com/spf13/viper"
)

type PVCSpec struct {
	StorageClassName string
	AccessModes      string
}
type TestConfig struct {
	ChannelName      string
	ChannelNamespace string
	GitURL           string
	Clusters         map[string]struct {
		KubeconfigPath string
	}
	PVCSpecs []PVCSpec
}

var config = &TestConfig{}

//nolint:cyclop
func ReadConfig(log logr.Logger, configFile string) error {
	viper.SetDefault("ChannelName", defaultChannelName)
	viper.SetDefault("ChannelNamespace", defaultChannelNamespace)
	viper.SetDefault("GitURL", defaultGitURL)

	if err := viper.BindEnv("ChannelName", "ChannelName"); err != nil {
		return (err)
	}

	if err := viper.BindEnv("ChannelNamespace", "ChannelNamespace"); err != nil {
		return (err)
	}

	if err := viper.BindEnv("GitURL", "GitURL"); err != nil {
		return (err)
	}

	if configFile == "" {
		log.Info("No configuration file specified, using default value config.yaml")

		configFile = "config.yaml"
	}

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

	return nil
}

func GetChannelName() string {
	return config.ChannelName
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
