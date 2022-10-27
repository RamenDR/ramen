#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck disable=2086
if ! command -v curl; then
	wget -O ${1}/curl https://github.com/moparisthebest/static-curl/releases/download/v7.76.0/curl-amd64
	chmod +x ${1}/curl
fi
