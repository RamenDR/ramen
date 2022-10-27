# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck shell=sh disable=2046,2086
docker_install()
{
	if test -w /var/run/docker.sock; then
		DOCKER_HOST=unix:///var/run/docker.sock
		return
	fi
	# https://docs.docker.com/engine/security/rootless/#install
	if ! command -v dockerd-rootless-setuptool.sh
	# TODO or less than version ${2}
	then
		$(dirname ${0})/curl-install.sh ${1}
		version_name=20.10.6
		curl -L https://download.docker.com/linux/static/stable/x86_64/docker-${version_name}.tgz | tar -xzvC${1} --strip-components 1
		curl -L https://download.docker.com/linux/static/stable/x86_64/docker-rootless-extras-${version_name}.tgz | tar -xzvC${1} --strip-components 1
		unset -v version_name
	fi
	$(dirname ${0})/uidmap-install.sh
	dockerd-rootless-setuptool.sh install
	# shellcheck disable=2034
	DOCKER_HOST=unix:///run/user/$(id -u)/docker.sock
}
