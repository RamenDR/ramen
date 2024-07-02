#!/bin/bash
set -e

script_dir="$(cd "$(dirname "$0")" && pwd)"

os=$(go env GOOS)
arch=$(go env GOARCH)
required_version="v1.34.2"
source_url="https://github.com/operator-framework/operator-sdk/releases/download/${required_version}/operator-sdk_${os}_${arch}"
target_dir="${script_dir}/../bin"
target_path="${target_dir}/operator-sdk"
tool="operator-sdk"

# sample output to parse: 'operator-sdk version: "v1.24.0", commit: "de6a14d03de3c36dcc9de3891af788b49d15f0f3", kubernetes version: "1.24.2", go version: "go1.18.6", GOOS: "linux", GOARCH: "amd64"'
installed_version=$("${target_path}" version | awk '{print $3}' | tr -d ',"')

if [ "$required_version" == "$installed_version" ]; then
  exit 0
fi

if [ -n "$installed_version" ]; then
  echo "Incorrect version of ${tool} found, installed version: ${installed_version}, expecting: ${required_version}"
  rm -f "${target_path}"
fi

echo "Installing ${tool}:${required_version} in ${target_dir}"
mkdir -p "${target_dir}"
curl --silent --show-error --location --output "${target_path}" "${source_url}"
chmod +x "${target_path}"
