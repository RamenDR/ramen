#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck disable=2046,2086
if ! command -v kubectl\
||\
        test $(kubectl version --client --short|cut -dv -f2|cut -d. -f1) -lt ${2}\
||\
{
        test $(kubectl version --client --short|cut -dv -f2|cut -d. -f1) -eq ${2}\
        &&\
        test $(kubectl version --client --short|cut -dv -f2|cut -d. -f2) -lt ${3}
}
then
	# https://kubernetes.io/docs/tasks/tools/install-kubectl-linux/#install-kubectl-binary-with-curl-on-linux
	$(dirname ${0})/curl-install.sh ${1}
	curl -LRo ${1}/kubectl https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl
	chmod +x ${1}/kubectl
fi
