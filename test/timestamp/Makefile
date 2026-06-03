# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# Use auto toolchain so go can satisfy go.mod on distros that default to local.
export GOTOOLCHAIN := auto

.PHONY: timestamp test

timestamp:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o timestamp main.go

test: timestamp
	go test -v .
