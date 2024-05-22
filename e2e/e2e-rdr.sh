#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

echo "Running tests..."

go test -kubeconfig-c1 ~/.config/drenv/rdr-rdr/kubeconfigs/rdr-dr1  -kubeconfig-c2 ~/.config/drenv/rdr-rdr/kubeconfigs/rdr-dr2 -kubeconfig-hub ~/.config/drenv/rdr-rdr/kubeconfigs/rdr-hub -timeout 0 -v "$@"
