#! /bin/bash

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

set -e -o pipefail

KIND_IMAGE="${KIND_IMAGE:-1.35.1@sha256:05d7bcdefbda08b4e038f644c4df690cdac3fba8b06f8289f30e10026720a1ab}"
KIND_VERSION="${KIND_VERSION:-v0.31.0}"
KIND_DIR="$(mktemp -d --tmpdir kind-XXXXXX)"
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-"$(basename "${KIND_DIR}" | tr "[:upper:]" "[:lower:]")"}"
rmdir "${KIND_DIR}"

scriptdir="$(dirname "$(realpath "$0")")"
cd "$scriptdir"

KIND_BIN="${scriptdir}/../bin/kind"
mkdir -p "${scriptdir}/../bin"

if [[ ! -x ${KIND_BIN} ]]; then
	curl -L -o kind https://github.com/kubernetes-sigs/kind/releases/download/"${KIND_VERSION}"/kind-linux-amd64
	install ./kind "${KIND_BIN}"
	rm ./kind
elif [[ ${KIND_VERSION} != v"$(${KIND_BIN} --version | cut -f 3 -d ' ')" ]]; then
	echo "Incorrect kind version ($(${KIND_BIN} --version)) found in ${KIND_BIN}, expecting ${KIND_VERSION}"
	exit 1
fi

${KIND_BIN} delete cluster --name "${KIND_CLUSTER_NAME}" || true
${KIND_BIN} create cluster --name "${KIND_CLUSTER_NAME}" --image "kindest/node:v${KIND_IMAGE}" --wait 5m

echo "${KIND_CLUSTER_NAME}"
#kubectl config use-context kind-"${KIND_CLUSTER_NAME}"
#${KIND_BIN} delete cluster --name "${KIND_CLUSTER_NAME}" || true
