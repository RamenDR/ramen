#!/bin/sh
# shellcheck disable=1090,2046,2086
set -x
set -e
ramen_hack_directory_path_name=$(dirname $0)
. $ramen_hack_directory_path_name/exit_stack.sh
. $ramen_hack_directory_path_name/until_true_or_n.sh
exit_stack_push unset -f until_true_or_n
exit_stack_push unset -v ramen_hack_directory_path_name
rook_ceph_deploy()
{
	PROFILE=$1 $ramen_hack_directory_path_name/minikube-rook-setup.sh create
}
exit_stack_push unset -f rook_ceph_deploy
rook_ceph_mirrors_deploy()
{
	PRIMARY_CLUSTER=$1 SECONDARY_CLUSTER=$2 $ramen_hack_directory_path_name/minikube-rook-mirror-setup.sh
	PRIMARY_CLUSTER=$2 SECONDARY_CLUSTER=$1 $ramen_hack_directory_path_name/minikube-rook-mirror-setup.sh
	PRIMARY_CLUSTER=$1 SECONDARY_CLUSTER=$2 $ramen_hack_directory_path_name/minikube-rook-mirror-test.sh
	PRIMARY_CLUSTER=$2 SECONDARY_CLUSTER=$1 $ramen_hack_directory_path_name/minikube-rook-mirror-test.sh
}
exit_stack_push unset -f rook_ceph_mirrors_deploy
rook_ceph_undeploy()
{
	PROFILE=$1 $ramen_hack_directory_path_name/minikube-rook-setup.sh delete
}
exit_stack_push unset -f rook_ceph_undeploy
minio_deploy()
{
	kubectl --context $1 apply -f $ramen_hack_directory_path_name/minio-deployment.yaml
}
exit_stack_push unset -f minio_deploy
minio_undeploy()
{
	kubectl --context $1 delete -f $ramen_hack_directory_path_name/minio-deployment.yaml
}
exit_stack_push unset -f minio_undeploy
ramen_image_directory_name=${ramen_image_directory_name-localhost}
ramen_image_name=${ramen_image_name-ramen-operator}
ramen_image_tag=${ramen_image_tag-v0.N}
ramen_image_name_colon_tag=${ramen_image_directory_name}/${ramen_image_name}:${ramen_image_tag}
exit_stack_push unset -v ramen_image_name_colon_tag ramen_image_tag ramen_image_name ramen_image_directory_name
ramen_build()
{
	${ramen_hack_directory_path_name}/docker-uninstall.sh ${HOME}/.local/bin
	. ${ramen_hack_directory_path_name}/podman-docker-install.sh
	. ${ramen_hack_directory_path_name}/go-install.sh; go_install ${HOME}/.local; unset -f go_install
	make -C ${1} docker-build IMG=${ramen_image_name_colon_tag}
	ramen_archive
}
exit_stack_push unset -f ramen_build
ramen_archive()
{
	set -- ${HOME}/.minikube/cache/images/${ramen_image_directory_name}
	mkdir -p ${1}
	set -- ${1}/${ramen_image_name}_${ramen_image_tag}
	# docker-archive doesn't support modifying existing images
	rm -f ${1}
	docker save ${ramen_image_name_colon_tag} -o ${1}
}
exit_stack_push unset -f ramen_archive
kube_context_set()
{
	exit_stack_push kubectl config use-context $(kubectl config current-context)
	kubectl config use-context ${1}
}
exit_stack_push unset -f kube_context_set
kube_context_set_undo()
{
	exit_stack_pop
}
exit_stack_push unset -f kube_context_set_undo
ramen_deploy()
{
	minikube -p ${2} image load ${ramen_image_name_colon_tag}
	kube_context_set ${2}
	make -C $1 deploy-$3 IMG=$ramen_image_name_colon_tag
	kube_context_set_undo
	kubectl --context ${2} -n ramen-system wait deployments --all --for condition=available --timeout 60s
	# Add s3 profile to ramen config
	cat <<EOF | kubectl --context ${2} apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: s3secret
  namespace: ramen-system
stringData:
  AWS_ACCESS_KEY_ID: "minio"
  AWS_SECRET_ACCESS_KEY: "minio123"
EOF
	ramen_config_map_name=ramen-${3}-operator-config
	until_true_or_n 90 kubectl --context ${2} -n ramen-system get configmap ${ramen_config_map_name}
	cp ${1}/config/${3}/manager/ramen_manager_config.yaml /tmp/ramen_manager_config.yaml
	cat <<-EOF >> /tmp/ramen_manager_config.yaml

	s3StoreProfiles:
	- s3ProfileName: $s3_store_profile_name
	  s3BucketName: $s3_store_profile_name
	  s3CompatibleEndpoint: $(minikube --profile=$s3_store_cluster_name -n minio service --url minio)
	  s3Region: us-east-1
	  s3SecretRef:
	    name: s3secret
	    namespace: ramen-system
	EOF

	kubectl --context ${2} -n ramen-system\
		create configmap ${ramen_config_map_name}\
		--from-file=/tmp/ramen_manager_config.yaml -o yaml --dry-run=client |
		kubectl --context ${2} -n ramen-system replace -f -
	unset -v ramen_config_map_name
}
exit_stack_push unset -f ramen_deploy
ramen_deploy_hub()
{
	ramen_deploy $1 $2 hub
}
exit_stack_push unset -f ramen_deploy_hub
ramen_deploy_spoke()
{
	ramen_deploy $1 $2 dr-cluster
}
exit_stack_push unset -f ramen_deploy_spoke
ramen_undeploy()
{
	kube_context_set ${2}
	make -C $1 undeploy-$3
	# Error from server (NotFound): error when deleting "STDIN": namespaces "ramen-system" not found
	# Error from server (NotFound): error when deleting "STDIN": serviceaccounts "ramen-hub-operator" not found
	# Error from server (NotFound): error when deleting "STDIN": roles.rbac.authorization.k8s.io "ramen-hub-leader-election-role" not found
	# Error from server (NotFound): error when deleting "STDIN": rolebindings.rbac.authorization.k8s.io "ramen-hub-leader-election-rolebinding" not found
	# Error from server (NotFound): error when deleting "STDIN": configmaps "ramen-hub-operator-config" not found
	# Error from server (NotFound): error when deleting "STDIN": services "ramen-hub-operator-metrics-service" not found
	# Error from server (NotFound): error when deleting "STDIN": deployments.apps "ramen-hub-operator" not found
	# Makefile:149: recipe for target 'undeploy-hub' failed
	# make: *** [undeploy-hub] Error 1
	kube_context_set_undo
	minikube -p $2 ssh docker image rm $ramen_image_name_colon_tag
	# Error: No such image: $ramen_image_name_colon_tag
	# ssh: Process exited with status 1
}
exit_stack_push unset -f ramen_undeploy
ramen_undeploy_hub()
{
	ramen_undeploy $1 $2 hub
}
exit_stack_push unset -f ramen_undeploy_hub
ramen_undeploy_spoke()
{
	ramen_undeploy $1 $2 dr-cluster
}
exit_stack_push unset -f ramen_undeploy_spoke
ocm_ramen_samples_git_ref=${ocm_ramen_samples_git_ref-main}
ocm_ramen_samples_git_path=${ocm_ramen_samples_git_path-ramendr}
exit_stack_push unset -v ocm_ramen_samples_git_ref
application_sample_deploy()
{
	set -- /tmp/$USER/ocm-ramen-samples/subscriptions $spoke_cluster_names
	mkdir -p $1
	cat <<-a >$1/kustomization.yaml
	resources:
	  - https://github.com/$ocm_ramen_samples_git_path/ocm-ramen-samples/subscriptions?ref=$ocm_ramen_samples_git_ref
	patchesJson6902:
	  - target:
	      group: ramendr.openshift.io
	      version: v1alpha1
	      kind: DRPolicy
	      name: dr-policy
	    patch: |-
	      - op: replace
	        path: /spec/drClusterSet
	        value:
	          - name: $2
	            s3ProfileName: $s3_store_profile_name
	          - name: $3
	            s3ProfileName: $s3_store_profile_name
	a
	kubectl --context $hub_cluster_name apply -k $1
	set --
	kubectl --context ${hub_cluster_name} -n ramen-samples get channels/ramen-gitops
	kubectl --context $hub_cluster_name apply -k https://github.com/$ocm_ramen_samples_git_path/ocm-ramen-samples/subscriptions/busybox?ref=$ocm_ramen_samples_git_ref
	kubectl --context ${hub_cluster_name} -n busybox-sample get placementrules/busybox-placement
	until_true_or_n 90 eval test \"\$\(kubectl --context ${hub_cluster_name} -n busybox-sample get subscriptions/busybox-sub -ojsonpath='{.status.phase}'\)\" = Propagated
	until_true_or_n 1 eval test -n \"\$\(kubectl --context ${hub_cluster_name} -n busybox-sample get placementrules/busybox-placement -ojsonpath='{.status.decisions[].clusterName}'\)\"
	set -- $(kubectl --context ${hub_cluster_name} -n busybox-sample get placementrules/busybox-placement -ojsonpath='{.status.decisions[].clusterName}')
	if test ${1} = ${hub_cluster_name}; then
		subscription_name_suffix=-local
	else
		unset -v subscription_name_suffix
	fi
	until_true_or_n 30 eval test \"\$\(kubectl --context ${1} -n busybox-sample get subscriptions/busybox-sub${subscription_name_suffix} -ojsonpath='{.status.phase}'\)\" = Subscribed
	unset -v subscription_name_suffix
	until_true_or_n 60 kubectl --context ${1} -n busybox-sample wait pods/busybox --for condition=ready --timeout 0
	until_true_or_n 30 eval test \"\$\(kubectl --context ${1} -n busybox-sample get persistentvolumeclaims/busybox-pvc -ojsonpath='{.status.phase}'\)\" = Bound
	date
	until_true_or_n 90 kubectl --context ${1} -n busybox-sample get volumereplicationgroups/busybox-drpc
	date
}
exit_stack_push unset -f application_sample_deploy
application_sample_undeploy()
{
	set -- $(kubectl --context ${hub_cluster_name} -n busybox-sample get placementrules/busybox-placement -ojsonpath='{.status.decisions[].clusterName}')
	kubectl --context ${1} delete persistentvolumes $(kubectl --context ${1} -n busybox-sample get persistentvolumeclaims/busybox-pvc -ojsonpath='{.spec.volumeName}') --wait=false
	kubectl --context $hub_cluster_name delete -k https://github.com/$ocm_ramen_samples_git_path/ocm-ramen-samples/subscriptions/busybox?ref=$ocm_ramen_samples_git_ref
	date
	set +e
	kubectl --context ${1} -n busybox-sample wait pods/busybox --for delete --timeout 2m
	# error: no matching resources found
	set -e
	date
	set +e
	kubectl --context ${1} -n busybox-sample wait volumereplicationgroups/busybox-drpc --for delete
	# error: no matching resources found
	set -e
	date
	kubectl --context $hub_cluster_name delete -k https://github.com/$ocm_ramen_samples_git_path/ocm-ramen-samples/subscriptions?ref=$ocm_ramen_samples_git_ref
	date
}
exit_stack_push unset -f application_sample_undeploy
ramen_directory_path_name=${ramen_hack_directory_path_name}/..
exit_stack_push unset -v ramen_directory_path_name
hub_cluster_name=${hub_cluster_name:-hub}
exit_stack_push unset -v hub_cluster_name
spoke_cluster_names=${spoke_cluster_names:-${hub_cluster_name}\ cluster1}
exit_stack_push unset -v spoke_cluster_names
cluster_names=$hub_cluster_name
for cluster_name in ${spoke_cluster_names}; do
	if test $cluster_name != $hub_cluster_name; then
		cluster_names=$cluster_names\ $cluster_name
	fi
done; unset -v cluster_name
exit_stack_push unset -v cluster_names
rook_ceph_deploy_all()
{
	# volumes required: mirror sources, mirror targets, minio backend
	for cluster_name in $cluster_names; do
		rook_ceph_deploy $cluster_name
	done; unset -v cluster_name
	rook_ceph_mirrors_deploy $spoke_cluster_names
}
exit_stack_push unset -v rook_ceph_deploy_all
rook_ceph_undeploy_all()
{
	for cluster_name in $cluster_names; do
		rook_ceph_undeploy $cluster_name
	done; unset -v cluster_name
}
exit_stack_push unset -v rook_ceph_undeploy_all
ramen_deploy_all()
{
	. ${ramen_hack_directory_path_name}/go-install.sh; go_install ${HOME}/.local; unset -f go_install
	ramen_deploy_hub $ramen_directory_path_name $hub_cluster_name
	for cluster_name in $spoke_cluster_names; do
		ramen_deploy_spoke $ramen_directory_path_name $cluster_name
	done; unset -v cluster_name
}
exit_stack_push unset -v ramen_deploy_all
ramen_undeploy_all()
{
	. ${ramen_hack_directory_path_name}/go-install.sh; go_install ${HOME}/.local; unset -f go_install
	for cluster_name in $spoke_cluster_names; do
		ramen_undeploy_spoke $ramen_directory_path_name $cluster_name
	done; unset -v cluster_name
	set +e # TODO remove once each resource is owned by hub or spoke but not both
	ramen_undeploy_hub $ramen_directory_path_name $hub_cluster_name
	set -e
}
exit_stack_push unset -v ramen_undeploy_all
s3_store_cluster_name=$hub_cluster_name
s3_store_profile_name=minio-on-$s3_store_cluster_name
exit_stack_push unset -v s3_store_profile_name s3_store_cluster_name
exit_stack_push unset -v command
# ENV variable to skip building ramen
#   - deploy will expect pre-loaded local docker image named
#     ${ramen_image_directory_name}/${ramen_image_name}:${ramen_image_tag}
skip_ramen_build=${skip_ramen_build:-false}
for command in "${@:-deploy}"; do
	case ${command} in
	deploy)
		hub_cluster_name=${hub_cluster_name} spoke_cluster_names=${spoke_cluster_names}\
		${ramen_hack_directory_path_name}/ocm-minikube.sh
		rook_ceph_deploy_all
		minio_deploy $s3_store_cluster_name
		if [ "$skip_ramen_build" = false ] ; then
		    ramen_build ${ramen_directory_path_name}
		fi
		ramen_deploy_all
		;;
	undeploy)
		ramen_undeploy_all
		minio_undeploy $s3_store_cluster_name
		rook_ceph_undeploy_all
		;;
	application_sample_deploy)
		application_sample_deploy
		;;
	application_sample_undeploy)
		application_sample_undeploy
		;;
	ramen_build)
		ramen_build ${ramen_directory_path_name}
		;;
	ramen_deploy)
		ramen_deploy_all
		;;
	ramen_undeploy)
		ramen_undeploy_all
		;;
	rook_ceph_deploy)
		rook_ceph_deploy_all
		;;
	rook_ceph_undeploy)
		rook_ceph_undeploy_all
		;;
	*)
		echo subcommand unsupported: ${command}
		;;
	esac
done
