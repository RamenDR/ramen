#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck disable=2086
if test "$(file -h /usr/bin/docker)" = '/usr/bin/docker: symbolic link to /usr/bin/podman'
then
	# shellcheck disable=1091
	. /etc/os-release
	case ${NAME} in
	Ubuntu)
		IFS=. read -r year month <<-a
		${VERSION_ID}
		a
		if test ${year} -gt 21 || { test ${year} -eq 21 && test ${month} -ge 10; }
		then
			sudo apt -y remove podman-docker
		else
			sudo rm -f /usr/bin/docker
		fi
		unset -v year month
		;;
	esac
fi
