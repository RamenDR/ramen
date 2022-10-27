#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

set -x
set -e
if test -x "${1}"/dockerd-rootless-setuptool.sh; then
	# https://docs.docker.com/engine/security/rootless/#uninstall
	"${1}"/dockerd-rootless-setuptool.sh uninstall
	rootlesskit rm -rf ~/.local/share/docker
	rm -f\
		"${1}"/containerd\
		"${1}"/containerd-shim\
		"${1}"/containerd-shim-runc-v2\
		"${1}"/ctr\
		"${1}"/docker\
		"${1}"/docker-init\
		"${1}"/docker-proxy\
		"${1}"/dockerd\
		"${1}"/dockerd-rootless-setuptool.sh\
		"${1}"/dockerd-rootless.sh\
		"${1}"/rootlesskit\
		"${1}"/rootlesskit-docker-proxy\
		"${1}"/runc\
		"${1}"/vpnkit
fi
