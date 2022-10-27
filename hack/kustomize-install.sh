#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck disable=2046,2086
if ! command -v kustomize; then
	$(dirname ${0})/curl-install.sh ${1}
        curl -L https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2Fv4.0.5/kustomize_v4.0.5_linux_amd64.tar.gz | tar -C${1} -xz
fi
