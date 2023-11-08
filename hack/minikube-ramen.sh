#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck disable=2086
set -e
ramen_hack_directory_path_name=$(dirname $0)
cluster_names=${cluster_names:-cluster1\ cluster2}
deploy() {
	spoke_cluster_names=$cluster_names $ramen_hack_directory_path_name/ocm-minikube.sh minikube_start_spokes
	hub_cluser_name=""\
	spoke_cluster_names=$cluster_names $ramen_hack_directory_path_name/ocm-minikube-ramen.sh\
		rook_ceph_deploy\
		cert_manager_deploy\
		minio_deploy_spokes\
		ramen_manager_image_build_and_archive\
		ramen_deploy_spokes\

}
undeploy() {
	hub_cluser_name=""\
	spoke_cluster_names=$cluster_names $ramen_hack_directory_path_name/ocm-minikube-ramen.sh\
		ramen_undeploy_spokes\
		minio_undeploy_spokes\
		cert_manager_undeploy\
		rook_ceph_undeploy\

}
manager_image_build() {
	$ramen_hack_directory_path_name/ocm-minikube-ramen.sh ramen_manager_image_build_and_archive
}
manager_image_deployed() {
	for cluster_name in $cluster_names; do
		minikube -p $cluster_name ssh -- docker images\|grep ramen
	done; unset -v cluster_name
}
manager_deploy() {
	spoke_cluster_names=$cluster_names $ramen_hack_directory_path_name/ocm-minikube-ramen.sh ramen_deploy_spokes
	manager_image_deployed
}
manager_undeploy() {
	spoke_cluster_names=$cluster_names $ramen_hack_directory_path_name/ocm-minikube-ramen.sh ramen_undeploy_spokes
}
manager_redeploy() {
	manager_undeploy&
	manager_image_build&
	wait
	manager_deploy
}
manager_log() {
	kubectl --context "$1" -nramen-system logs deploy/ramen-dr-cluster-operator manager
}
application_sample_namespace_name=${application_sample_namespace_name:-default}
application_sample_namespace_deploy() {
	kubectl create namespace $application_sample_namespace_name --dry-run=client -oyaml|kubectl --context $1 apply -f-
}
application_sample_namespace_undeploy() {
	kubectl --context "$1" delete namespace $application_sample_namespace_name
}
application_sample_yaml() {
	kubectl create --dry-run=client -oyaml --namespace "$application_sample_namespace_name" -k https://github.com/RamenDR/ocm-ramen-samples/busybox
}
application_sample_deploy() {
	application_sample_yaml|kubectl --context "$1" apply -f -
}
application_sample_undeploy() {
	application_sample_yaml|kubectl --context "$1" delete -f - --ignore-not-found
}
application_sample_vrg_yaml() {
	cat <<-a
	---
	apiVersion: ramendr.openshift.io/v1alpha1
	kind: VolumeReplicationGroup
	metadata:
	  name: bb
	  namespace: $2
	  labels:
	    $3
	spec:
	  async:
	    replicationClassSelector: {}
	    schedulingInterval: 1m
	  pvcSelector:
	    matchLabels:
	      appname: busybox
	  replicationState: $1
	  s3Profiles:
$(for cluster_name in $cluster_names; do echo \ \ -\ minio-on-$cluster_name; done; unset -v cluster_name)${vrg_appendix-}
	a
}
application_sample_vrg_deploy() {
	application_sample_vrg_yaml primary "$2" "$3"|kubectl --context "$1" apply -f -
}
application_sample_vrg_deploy_sec() {
	application_sample_vrg_yaml secondary "$2" "$3"|kubectl --context "$1" apply -f -
}
application_sample_vrg_undeploy() {
	application_sample_vrg_yaml primary "$2" "$3"|kubectl --context "$1" delete --ignore-not-found -f -
}
"${@:-deploy}"
unset -f application_sample_vrg_undeploy
unset -f application_sample_vrg_deploy
unset -f application_sample_vrg_kubectl
unset -f application_sample_undeploy
unset -f application_sample_deploy
unset -f application_sample_kubectl
unset -f application_sample_namespace_undeploy
unset -f application_sample_namespace_deploy
unset -v application_sample_namespace_name
unset -f manager_log
unset -f manager_redeploy
unset -f manager_undeploy
unset -f manager_deploy
unset -f manager_image_deployed
unset -f manager_image_build
unset -f undeploy
unset -f deploy
unset -v cluster_names
unset -v ramen_hack_directory_path_name
