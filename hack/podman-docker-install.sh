#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck disable=2046,2086
if ! command -v docker; then
	$(dirname ${0})/podman-install.sh
	# shellcheck disable=1091
	. /etc/os-release
	case ${NAME} in
	Ubuntu)
		IFS=. read -r year month <<-a
		${VERSION_ID}
		a
		# https://github.com/containers/podman/issues/1553#issuecomment-435984922
		# https://packages.ubuntu.com/search?suite=all&arch=amd64&keywords=podman-docker
		if test ${year} -gt 21 || { test ${year} -eq 21 && test ${month} -ge 10; }
		then
			sudo apt-get -y install podman-docker
		else
			sudo ln -s /usr/bin/podman /usr/bin/docker
		fi
		unset -v year month
		docker --version
		;;
	esac
fi
