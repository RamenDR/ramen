#!/bin/bash
set -e

script_dir="$(cd "$(dirname "$0")" && pwd)"

# Reference : https://sdk.operatorframework.io/docs/upgrading-sdk-version/v1.40.0/#envtest-version-automation-and-improved-test-binary-discovery
required_version=$(go list -m -f "{{ .Version }}" sigs.k8s.io/controller-runtime | awk -F'[v.]' '{printf "release-%d.%d", $2, $3}')
source_url="sigs.k8s.io/controller-runtime/tools/setup-envtest@${required_version}"
target_dir="${script_dir}/../testbin"
target_path="${target_dir}/setup-envtest"
k8s_version="1.33.0"

# The setup-envtest tool has no versioning, so we need to use the latest version.
# The go install command is fast enough that it can be run every time.
mkdir -p "${target_dir}"
GOBIN="${target_dir}" go install "${source_url}"

# Storing the path to the assets in a file so that it can be used by the test files.
# Making the name of the file version agnostic so that changing the version is easier.
kubebuilder_assets=$("${target_path}" use "${k8s_version}" --bin-dir "${target_dir}" --print path)
echo -n "${kubebuilder_assets}" > "${target_dir}/testassets.txt"
