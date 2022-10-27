#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck disable=2086
if ! command -v podman; then
	# https://podman.io/getting-started/installation#linux-distributions
	# shellcheck disable=1091
	. /etc/os-release
	case ${NAME} in
	Ubuntu)
		echo 'deb https://download.opensuse.org/repositories/devel:/kubic:/libcontainers:/stable/xUbuntu_'${VERSION_ID}'/ /' | sudo tee /etc/apt/sources.list.d/podman.list >/dev/null
		curl -L https://download.opensuse.org/repositories/devel:/kubic:/libcontainers:/stable/xUbuntu_${VERSION_ID}/Release.key | sudo apt-key add -
		sudo apt-get update
		#sudo apt-get -y upgrade
		sudo apt-get -y install podman
		podman --version
		;;
	esac
fi
