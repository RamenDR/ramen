#!/bin/bash
set -e

script_dir="$(cd "$(dirname "$0")" && pwd)"

source_url="https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh"
required_version="1.62.0"
target_dir="${script_dir}/../testbin"
target_path="${target_dir}/golangci-lint"
tool="golangci-lint"

# sample output to parse: 'golangci-lint has version 1.55.2 built with go1.21.3 from e3c2265f on 2023-11-03T12:59:25Z'
installed_version=$("${target_path}" version --format=short || true)

if [ "$required_version" == "$installed_version" ]; then
  exit 0
fi

if [ -n "$installed_version" ]; then
  echo "Incorrect version of ${tool} found, installed version: ${installed_version}, expecting: ${required_version}"
  rm -f "${target_path}"
fi

echo "Installing ${tool}:${required_version} in ${target_dir}"
mkdir -p "${target_dir}"
curl --silent --show-error --location --fail "$source_url" | sh -s -- -b "${target_dir}" v"$required_version"
