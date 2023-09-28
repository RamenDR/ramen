#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck disable=2086

cert_manager_kubectl_context() {
	kubectl --context $1 $2 -f https://github.com/cert-manager/cert-manager/releases/download/v1.13.1/cert-manager.yaml
}
cert_manager_deploy_context() {
	cert_manager_kubectl_context $1 apply
}
cert_manager_undeploy_context() {
	cert_manager_kubectl_context $1 delete\ --ignore-not-found
}
cert_manager_unset() {
	unset -f cert_manager_unset
	unset -f cert_manager_undeploy_context
	unset -f cert_manager_deploy_context
	unset -f cert_manager_kubectl_context
}
