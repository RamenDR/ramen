#!/bin/bash
set -e

script_dir="$(cd "$(dirname "$0")" && pwd)"

os=$(go env GOOS)
arch=$(go env GOARCH)
required_version="v1.43.0"
source_url="https://github.com/operator-framework/operator-registry/releases/download/${required_version}/${os}-${arch}-opm"
target_dir="${script_dir}/../bin"
target_path="${target_dir}/opm"
tool="opm"

# sample output to parse: 'Version: version.Version{OpmVersion:"v1.23.2", GitCommit:"82505333", BuildDate:"2022-07-04T13:45:39Z", GoOs:"linux", GoArch:"amd64"}'
installed_version=$("${target_path}" version | cut -d"{" -f2 | cut -d"," -f1 | cut -d":" -f2 | tr -d '"')

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
