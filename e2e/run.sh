#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

echo "Running tests..."

go test -timeout 0 -v "$@"
