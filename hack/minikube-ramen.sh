#!/bin/sh
# shellcheck disable=2086
set -e
ramen_hack_directory_path_name=$(dirname $0)
cluster_names=${cluster_names:-cluster1\ cluster2}
deploy() {
	spoke_cluster_names=$cluster_names $ramen_hack_directory_path_name/ocm-minikube.sh minikube_start_spokes
	spoke_cluster_names=$cluster_names $ramen_hack_directory_path_name/ocm-minikube-ramen.sh\
		rook_ceph_deploy\
		minio_deploy_spokes\
		ramen_manager_image_build_and_archive\
		ramen_deploy_spokes\

}
undeploy() {
	spoke_cluster_names=$cluster_names $ramen_hack_directory_path_name/ocm-minikube-ramen.sh\
		ramen_undeploy_spokes\
		minio_undeploy_spokes\
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
application_sample_namespace_name=${application_sample_namespace_name:-default}
application_sample_namespace_deploy() {
	kubectl create namespace $application_sample_namespace_name --dry-run=client -oyaml|kubectl --context $1 apply -f-
}
application_sample_namespace_undeploy() {
	kubectl --context $1 delete namespace $application_sample_namespace_name
}
application_sample_kubectl() {
	kubectl --context $1 -n$application_sample_namespace_name $2 -khttps://github.com/RamenDR/ocm-ramen-samples/busybox
}
application_sample_deploy() {
	application_sample_kubectl $1 apply
}
application_sample_undeploy() {
	application_sample_kubectl $1 delete
}
application_sample_vrg_kubectl() {
	cat <<-a|kubectl --context $1 -n$application_sample_namespace_name $3 -f-
	---
	apiVersion: ramendr.openshift.io/v1alpha1
	kind: VolumeReplicationGroup
	metadata:
	  name: bb
	spec:
	  async:
	    replicationClassSelector: {}
	    schedulingInterval: 1m
	  pvcSelector:
	    matchLabels:
	      appname: busybox
	  replicationState: $2
	  s3Profiles:
$(for cluster_name in $cluster_names; do echo \ \ -\ minio-on-$cluster_name; done; unset -v cluster_name)${vrg_appendix-}
	a
}
application_sample_vrg_deploy() {
	application_sample_vrg_kubectl $1 primary apply
}
application_sample_vrg_deploy_sec() {
	application_sample_vrg_kubectl $1 secondary apply
}
application_sample_vrg_undeploy() {
	application_sample_vrg_kubectl $1 primary delete\ --ignore-not-found
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
unset -f manager_redeploy
unset -f manager_undeploy
unset -f manager_deploy
unset -f manager_image_deployed
unset -f manager_image_build
unset -f undeploy
unset -f deploy
unset -v cluster_names
unset -v ramen_hack_directory_path_name
