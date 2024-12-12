#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

go test -c -o e2e
./e2e -test.timeout 0 -test.v "$@" 2>&1 | tee e2e.log
