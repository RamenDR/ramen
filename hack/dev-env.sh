#!/bin/bash
set -e

script_dir="$(cd "$(dirname "$0")" && pwd)"

if [[ $1 != "create" && $1 != "destroy" ]]; then
    echo "Usage: $0 create|destroy"
    exit 1
fi

if [[ "$VIRTUAL_ENV" == "" ]]; then
  echo "Virtual environment not activated"
  echo "Run the following command and try again"
  echo "make venv && source venv"
  exit 1
fi

cd "$script_dir"/..
cd test

if [[ $1 == "create" ]]; then
    drenv start envs/regional-dr.yaml
fi

if [[ $1 == "destroy" ]]; then
    drenv delete envs/regional-dr.yaml
fi
