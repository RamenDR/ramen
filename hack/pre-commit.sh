#! /bin/bash

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# vim: set ts=4 sw=4 et :

# Usage: pre-commit.sh

# Run checks from root of the repo
scriptdir="$(dirname "$(realpath "$0")")"
cd "$scriptdir/.." || exit 1

OUTPUTS_FILE="$(mktemp --tmpdir tool-errors-XXXXXX)"

echo "${OUTPUTS_FILE}"

check_version() {
    if ! [[ "$1" == "$(echo -e "$1\n$2" | sort -V | tail -n1)" ]] ; then
        echo "ERROR: $3 version is too old. Expected $2, found $1"
        exit 1
    fi
}

get_files() {
    git ls-files -z | grep --binary-files=without-match --null-data --null -E "$1"
}

# check_tool <tool>
check_tool() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "ERROR: $1 is not installed"
        echo "You can install it by running:"
        case "$1" in
            mdformat)
                echo "  pip install mdformat mdformat-gfm"
                ;;
            shellcheck)
                echo "  dnf install ShellCheck"
                ;;
            yamllint)
                echo "  dnf install yamllint"
                ;;
            *)
                echo "  unknown tool $1"
                ;;
        esac
        exit 1
    fi
}

# mdformat: https://github.com/executablebooks/mdformat
run_mdformat() {
    local tool="mdformat"
    local required_version="0.7.0"
    local detected_version

    echo "=====  $tool ====="

    check_tool "${tool}"

    detected_version=$("${tool}" --version | cut -d' ' -f2)
    check_version "${detected_version}" "${required_version}" "${tool}"

    get_files ".*\.md$" | xargs -0 -r "${tool}" --check | tee -a "${OUTPUTS_FILE}"
    echo
    echo
}

run_shellcheck() {
    local tool="shellcheck"
    local required_version="0.9.0"
    local detected_version

    echo "=====  $tool  ====="

    check_tool "${tool}"

    detected_version=$("${tool}" --version | grep "version:" | cut -d' ' -f2)
    check_version "${detected_version}" "${required_version}" "${tool}"

    get_files '.*\.(ba)?sh' | xargs -0 -r "${tool}" | tee -a "${OUTPUTS_FILE}"
    echo
    echo
}

run_yamllint() {
    local tool="yamllint"
    local required_version="1.35.0"
    local detected_version

    echo "=====  $tool  ====="

    check_tool "${tool}"

    detected_version=$("${tool}" -v | cut -d' ' -f2)
    check_version "${detected_version}" "${required_version}" "${tool}"

    get_files '.*\.ya?ml' | xargs -0 -r "${tool}" -s -c "${scriptdir}/yamlconfig.yaml" | tee -a "${OUTPUTS_FILE}"
    echo
    echo
}


run_mdformat
run_shellcheck
run_yamllint

# Fail if any of the tools reported errors
(! < "${OUTPUTS_FILE}" read -r)
