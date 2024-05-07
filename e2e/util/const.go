// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import "os"

const (
	RamenSystemNamespace = "ramen-system"

	Timeout      = 600 // seconds
	TimeInterval = 30  // seconds

	defaultChannelName      = "ramen-gitops"
	defaultChannelNamespace = "ramen-samples"
	defaultChannelPathname  = "https://github.com/RamenDR/ocm-ramen-samples.git"
)

func GetChannelName() string {
	return getEnv("ChannelName", defaultChannelName)
}

func GetChannelNamespace() string {
	return getEnv("ChannelNamespace", defaultChannelNamespace)
}

func GetChannelPathname() string {
	return getEnv("ChannelPathname", defaultChannelPathname)
}

func getEnv(name, defaultValue string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	return defaultValue
}
