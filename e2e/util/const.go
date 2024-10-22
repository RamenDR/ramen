// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import "time"

const (
	RamenSystemNamespace = "ramen-system"

	Timeout       = 600 * time.Second
	RetryInterval = 5 * time.Second

	defaultChannelName      = "ramen-gitops"
	defaultChannelNamespace = "ramen-samples"
	defaultGitURL           = "https://github.com/RamenDR/ocm-ramen-samples.git"

	ArgocdNamespace = "argocd"
	RamenOpsNs      = "ramen-ops"
)
