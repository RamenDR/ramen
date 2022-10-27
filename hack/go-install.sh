# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck shell=sh disable=2046,2086
go_install()
{
	if ! command -v go
	# TODO or version less than ${2}
	#|| $(go version | { read _ _ v _; echo ${v#go}; })
	then
		PATH=${1}/go/bin:${PATH}
		if ! command -v go
		then
			mkdir -p ${1}/bin
			$(dirname ${0})/curl-install.sh ${1}/bin
			curl -L https://golang.org/dl/go1.16.2.linux-amd64.tar.gz | tar -C${1} -xz
		fi
	fi
}
