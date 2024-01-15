#!/bin/bash
set -e

script_dir="$(cd "$(dirname "$0")" && pwd)"

required_version="v4.5.7"
os=$(go env GOOS)
arch=$(go env GOARCH)
source_url="https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2F${required_version}/kustomize_${required_version}_${os}_${arch}.tar.gz"
target_dir="${script_dir}/../bin"
target_path="${target_dir}/kustomize"
tool="kustomize"

# sample output to parse: '{Version:kustomize/v4.5.7 GitCommit:56d82a8378dfc8dc3b3b1085e5a6e67b82966bd7 BuildDate:2022-08-02T16:35:54Z GoOs:linux GoArch:amd64}'
installed_version=$("${target_path}" version | tr -d "{" | tr -d '}' | cut -d" " -f1 | cut -d"/" -f2)

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
tar -xzf "${target_path}" -C "${target_dir}"
chmod +x "${target_path}"
