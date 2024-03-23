#!/bin/bash

script_dir="$(cd "$(dirname "$0")" && pwd)"
cd "${script_dir}" || exit 1

if ! ./ramen_e2e run-default-suites; then
  echo "Test failed"
  exit 1
fi