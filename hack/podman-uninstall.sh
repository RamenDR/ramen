#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck disable=2046,2086
if command -v podman; then
	$(dirname ${0})/podman-docker-uninstall.sh
	# shellcheck disable=1091
	. /etc/os-release
	case ${NAME} in
	Ubuntu)
		sudo apt -y remove podman
		;;
	esac
fi
