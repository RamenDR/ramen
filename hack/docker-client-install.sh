#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck shell=sh disable=2046,2086
set -x
set -- $1 20.10.6
if command -v $1/docker; then
	IFS=. read -r x y z <<-a
	$($1/docker version --format '{{.Client.Version}}' 2>/dev/null)
	a
	IFS=. read -r x1 y1 z1 <<-a
	$2
	a
	if test $x -gt $x1 || { test $x -eq $x1 && { test $y -gt $y1 || { test $y -eq $y1 && test $z -ge $z1;};};}; then
		unset -v x y z x1 y1 z1
		exit
	fi
	unset -v x y z x1 y1 z1
fi
$(dirname $0)/curl-install.sh $1
curl -L https://download.docker.com/linux/static/stable/x86_64/docker-$2.tgz | tar -xzvC$1 --strip-components 1 docker/docker
