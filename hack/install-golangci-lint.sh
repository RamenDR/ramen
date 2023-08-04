#!/bin/bash

GOLANGCI_URL="https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh"
GOLANGCI_VERSION="1.49.0"

# Get the installed version of golangci-lint, if available
GOLANGCI_INSTALLED_VER=$(testbin/golangci-lint version --format=short 2>/dev/null)

# Check if golangci-lint is not installed or if the version does not match
if [ -z "$GOLANGCI_INSTALLED_VER" ] || [ "$GOLANGCI_VERSION" != "$GOLANGCI_INSTALLED_VER" ]; then
  if [ -n "$GOLANGCI_INSTALLED_VER" ]; then
    echo "Incorrect version ($GOLANGCI_INSTALLED_VER) of golangci-lint found, expecting $GOLANGCI_VERSION"
    rm -f testbin/golangci-lint
  fi

  # Install the correct version
  echo "Installing golangci-lint (version: $GOLANGCI_VERSION) into testbin"
  curl -sSfL "$GOLANGCI_URL" | sh -s -- -b testbin v"$GOLANGCI_VERSION"
fi
