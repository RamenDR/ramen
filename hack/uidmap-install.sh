#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck disable=1091
if ! command -v newuidmap
then
	. /etc/os-release
	case ${NAME} in
	"Ubuntu")
		set +e
		sudo apt-get install -y uidmap
		# dpkg: error processing package lvm2 (--configure):
		# installed lvm2 package post-installation script subprocess returned error exit status 1
		set -e
		;;
	esac
fi
