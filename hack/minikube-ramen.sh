#!/bin/sh
# shellcheck disable=2086
set -x
set -e
ramen_hack_directory_path_name=$(dirname $0)
cluster_names=${cluster_names:-cluster1\ cluster2}
deploy()
{
	spoke_cluster_names=$cluster_names $ramen_hack_directory_path_name/ocm-minikube.sh minikube_start_spokes
	spoke_cluster_names=$cluster_names $ramen_hack_directory_path_name/ocm-minikube-ramen.sh\
		rook_ceph_deploy\
		minio_deploy_spokes\
		ramen_manager_image_build_and_archive\
		ramen_deploy_spokes\

}
undeploy()
{
	spoke_cluster_names=$cluster_names $ramen_hack_directory_path_name/ocm-minikube-ramen.sh\
		ramen_undeploy_spokes\
		minio_undeploy_spokes\
		rook_ceph_undeploy\

}
manager_redeploy()
{
	spoke_cluster_names=$cluster_names $ramen_hack_directory_path_name/ocm-minikube-ramen.sh\
		ramen_undeploy_spokes\
		ramen_manager_image_build_and_archive\
		ramen_deploy_spokes\

	for cluster_name in $cluster_names; do
		minikube -p $cluster_name ssh -- docker images\|grep ramen
	done; unset -v cluster_name
}
for command in "${@:-deploy}"; do
	$command
done
unset -v command
unset -f manager_redeploy
unset -f undeploy
unset -f deploy
unset -v cluster_names
unset -v ramen_hack_directory_path_name
