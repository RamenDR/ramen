// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"fmt"

	"github.com/spf13/viper"
)

type TestConfig struct {
	ChannelName      string
	ChannelNamespace string
	GitURL           string
}

var config = &TestConfig{}

func ReadConfig() error {
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

	viper.SetConfigFile("config.yaml")

	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}

	if err := viper.Unmarshal(config); err != nil {
		return fmt.Errorf("failed to unmarshal config: %v", err)
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
