#!/bin/bash
set -e

script_dir="$(cd "$(dirname "$0")" && pwd)"

required_version="v0.14.0"
source_url="sigs.k8s.io/controller-tools/cmd/controller-gen@${required_version}"
target_dir="${script_dir}/../bin"
target_path="${target_dir}/controller-gen"
tool="controller-gen"

# sample output to parse: 'Version: v0.14.0'
installed_version=$("${target_path}" --version | cut -d" " -f2)

if [ "$required_version" == "$installed_version" ]; then
  exit 0
fi

if [ -n "$installed_version" ]; then
  echo "Incorrect version of ${tool} found, installed version: ${installed_version}, expecting: ${required_version}"
  rm -f "${target_path}"
fi

echo "Installing ${tool}:${required_version} in ${target_dir}"
mkdir -p "${target_dir}"
GOBIN="${target_dir}" go install "${source_url}"
