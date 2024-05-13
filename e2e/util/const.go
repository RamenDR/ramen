// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"os"
)

const (
	RamenSystemNamespace = "ramen-system"

	Timeout      = 600 // seconds
	TimeInterval = 30  // seconds

	defaultChannelName      = "ramen-gitops"
	defaultChannelNamespace = "ramen-samples"
	defaultGitURL           = "https://github.com/RamenDR/ocm-ramen-samples.git"
)

func GetChannelName() string {
	channelName, err := GetChannelNameFromYaml()
	if err == nil && channelName != "" {
		return channelName
	}

	return getEnv("ChannelName", defaultChannelName)
}

func GetChannelNamespace() string {
	channelNamespace, err := GetChannelNamespaceFromYaml()
	if err == nil && channelNamespace != "" {
		return channelNamespace
	}

	return getEnv("ChannelNamespace", defaultChannelNamespace)
}

func GetGitURL() string {
	gitURL, err := GetGitURLFromYaml()
	if err == nil && gitURL != "" {
		return gitURL
	}

	return getEnv("GitURL", defaultGitURL)
}

func getEnv(name, defaultValue string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	return defaultValue
}
