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

# run_check <file_regex> <checker_exe> [optional args to checker...]
function run_check() {
    regex="$1"
    shift
    exe="$1"
    shift

    if [ -x "$(command -v "$exe")" ]; then
        echo "=====  $exe  ====="
        find . \
            -path ./testbin -prune -o \
            -path ./bin -prune -o \
            -regextype egrep -iregex "$regex" -print0 | \
            xargs -0r "$exe" "$@" 2>&1 | tee -a "${OUTPUTS_FILE}"
        echo
        echo
    else
        echo "FAILED: All checks required, but $exe not found!"
        exit 1
    fi
}

# markdownlint: https://github.com/markdownlint/markdownlint
# https://github.com/markdownlint/markdownlint/blob/master/docs/RULES.md
# Install via: gem install mdl
run_check '.*\.md' mdl --style "${scriptdir}/mdl-style.rb"

# Install via: dnf install ShellCheck
run_check '.*\.(ba)?sh' shellcheck

# Install via: dnf install yamllint
run_check '.*\.ya?ml' yamllint -s -c "${scriptdir}/yamlconfig.yaml"

(! < "${OUTPUTS_FILE}" read -r)
