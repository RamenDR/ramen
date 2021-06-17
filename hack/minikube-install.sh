#!/bin/sh
# shellcheck disable=2046,2086
if ! command -v minikube; then
	# https://minikube.sigs.k8s.io/docs/start/
	$(dirname ${0})/curl-install.sh ${1}
	minikube_version=latest
	minikube_version=v1.18.1
	curl -LRo ${1}/minikube https://storage.googleapis.com/minikube/releases/${minikube_version}/minikube-linux-amd64
	unset -v minikube_version
	chmod +x ${1}/minikube
fi
