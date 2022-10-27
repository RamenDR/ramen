#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck disable=2086
set -x
# https://github.com/containers/fuse-overlayfs
if ! command -v fuse-overlayfs; then
	# shellcheck disable=1091
	. /etc/os-release
	case $NAME in
	Ubuntu)
		IFS=. read -r year month <<-a
		$VERSION_ID
		a
		# sudo apt-get update
		if test $year -gt 19 || { test $year -eq 19 && test $month -ge 04; }
		then
			sudo apt-get -y install fuse-overlayfs
		else
			sudo apt-get -y install buildah
			sudo apt-get -y install libfuse2
			ls /dev/fuse
			fuse_overlayfs_directory_path_name=/tmp/fuse-overlayfs
			git clone https://github.com/containers/fuse-overlayfs $fuse_overlayfs_directory_path_name
			buildah bud -v $fuse_overlayfs_directory_path_name:/build/fuse-overlayfs -t fuse-overlayfs -f $fuse_overlayfs_directory_path_name/Containerfile.static.ubuntu $fuse_overlayfs_directory_path_name
			sudo cp $fuse_overlayfs_directory_path_name/fuse-overlayfs /usr/bin
			unset -v fuse_overlayfs_directory_path_name
			sudo rm -rf ~/.local/share/containers
		fi
		;;
	esac
fi
