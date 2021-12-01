#!/bin/sh
# shellcheck disable=1090,2046,2086
set -x
set -e
ramen_hack_directory_path_name=$(dirname $0)
. $ramen_hack_directory_path_name/exit_stack.sh
. $ramen_hack_directory_path_name/true_if_exit_status_and_stderr.sh
exit_stack_push unset -f true_if_exit_status_and_stderr
. $ramen_hack_directory_path_name/until_true_or_n.sh
exit_stack_push unset -f until_true_or_n
exit_stack_push unset -v ramen_hack_directory_path_name
rook_ceph_deploy_spoke()
{
	PROFILE=$1 $ramen_hack_directory_path_name/minikube-rook-setup.sh create
}
exit_stack_push unset -f rook_ceph_deploy_spoke
rook_ceph_mirrors_deploy()
{
	PRIMARY_CLUSTER=$1 SECONDARY_CLUSTER=$2 $ramen_hack_directory_path_name/minikube-rook-mirror-setup.sh
	PRIMARY_CLUSTER=$2 SECONDARY_CLUSTER=$1 $ramen_hack_directory_path_name/minikube-rook-mirror-setup.sh
	PRIMARY_CLUSTER=$1 SECONDARY_CLUSTER=$2 $ramen_hack_directory_path_name/minikube-rook-mirror-test.sh
	PRIMARY_CLUSTER=$2 SECONDARY_CLUSTER=$1 $ramen_hack_directory_path_name/minikube-rook-mirror-test.sh
}
exit_stack_push unset -f rook_ceph_mirrors_deploy
rook_ceph_undeploy_spoke()
{
	PROFILE=$1 $ramen_hack_directory_path_name/minikube-rook-setup.sh delete
}
exit_stack_push unset -f rook_ceph_undeploy_spoke
minio_deploy()
{
	for cluster_name in $spoke_cluster_names; do
		kubectl --context $cluster_name apply -f $ramen_hack_directory_path_name/minio-deployment.yaml
		date
		kubectl --context $cluster_name -n minio wait deployments/minio --for condition=available --timeout 60s
		date
	done; unset -v cluster_name
}
exit_stack_push unset -f minio_deploy
minio_undeploy()
{
	for cluster_name in $spoke_cluster_names; do
		kubectl --context $cluster_name delete -f $ramen_hack_directory_path_name/minio-deployment.yaml
	done; unset -v cluster_name
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
	make -C $ramen_directory_path_name docker-build IMG=$ramen_image_name_colon_tag
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
ramen_deploy_hub_or_spoke()
{
	minikube -p $1 image load $ramen_image_name_colon_tag
	. $ramen_hack_directory_path_name/go-install.sh; go_install $HOME/.local; unset -f go_install
	kube_context_set $1
	make -C $ramen_directory_path_name deploy-$2 IMG=$ramen_image_name_colon_tag
	kube_context_set_undo
	kubectl --context $1 -n ramen-system wait deployments --all --for condition=available --timeout 60s
	# Add s3 profile to ramen config
	cat <<-EOF | kubectl --context $1 apply -f -
	apiVersion: v1
	kind: Secret
	metadata:
	  name: s3secret
	  namespace: ramen-system
	stringData:
	  AWS_ACCESS_KEY_ID: "minio"
	  AWS_SECRET_ACCESS_KEY: "minio123"
	EOF
	ramen_config_map_name=ramen-$2-operator-config
	until_true_or_n 90 kubectl --context $1 -n ramen-system get configmap $ramen_config_map_name
	cp $ramen_directory_path_name/config/$2/manager/ramen_manager_config.yaml /tmp/ramen_manager_config.yaml
	set -- $1 $2 $spoke_cluster_names
	cat <<-EOF >>/tmp/ramen_manager_config.yaml
	s3StoreProfiles:
	- s3ProfileName: minio-on-$3
	  s3BucketName: ramen
	  s3CompatibleEndpoint: $(minikube --profile $3 -n minio service --url minio)
	  s3Region: us-east-1
	  s3SecretRef:
	    name: s3secret
	    namespace: ramen-system
	- s3ProfileName: minio-on-$4
	  s3BucketName: ramen
	  s3CompatibleEndpoint: $(minikube --profile $4 -n minio service --url minio)
	  s3Region: us-west-1
	  s3SecretRef:
	    name: s3secret
	    namespace: ramen-system
	EOF

	kubectl --context $1 -n ramen-system\
		create configmap ${ramen_config_map_name}\
		--from-file=/tmp/ramen_manager_config.yaml -o yaml --dry-run=client |
		kubectl --context $1 -n ramen-system replace -f -
	unset -v ramen_config_map_name
}
exit_stack_push unset -f ramen_deploy_hub_or_spoke
ramen_deploy_hub()
{
	ramen_deploy_hub_or_spoke $hub_cluster_name hub
	ramen_samples_channel_and_drpolicy_deploy
}
exit_stack_push unset -f ramen_deploy_hub
ramen_deploy_spoke()
{
	ramen_deploy_hub_or_spoke $1 dr-cluster
}
exit_stack_push unset -f ramen_deploy_spoke
ramen_undeploy_hub_or_spoke()
{
	kube_context_set $1
	make -C $ramen_directory_path_name undeploy-$2
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
	minikube -p $1 ssh -- docker image rm $ramen_image_name_colon_tag
	# Error: No such image: $ramen_image_name_colon_tag
	# ssh: Process exited with status 1
}
exit_stack_push unset -f ramen_undeploy_hub_or_spoke
ramen_undeploy_hub()
{
	ramen_samples_channel_and_drpolicy_undeploy
	ramen_undeploy_hub_or_spoke $hub_cluster_name hub
}
exit_stack_push unset -f ramen_undeploy_hub
ramen_undeploy_spoke()
{
	ramen_undeploy_hub_or_spoke $1 dr-cluster
}
exit_stack_push unset -f ramen_undeploy_spoke
ocm_ramen_samples_git_ref=${ocm_ramen_samples_git_ref-main}
ocm_ramen_samples_git_path=${ocm_ramen_samples_git_path-ramendr}
exit_stack_push unset -v ocm_ramen_samples_git_ref
ramen_samples_channel_and_drpolicy_deploy()
{
	set -- ocm-ramen-samples/subscriptions
	set -- /tmp/$USER/$1 $1 $spoke_cluster_names
	mkdir -p $1
	cat <<-a >$1/kustomization.yaml
	resources:
	  - https://github.com/$ocm_ramen_samples_git_path/$2?ref=$ocm_ramen_samples_git_ref
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
	          - name: $3
	            s3ProfileName: minio-on-$3
	          - name: $4
	            s3ProfileName: minio-on-$4
	a
	kubectl --context $hub_cluster_name apply -k $1
	kubectl --context $hub_cluster_name -n ramen-samples get channels/ramen-gitops
}
exit_stack_push unset -f ramen_samples_channel_and_drpolicy_deploy
ramen_samples_channel_and_drpolicy_undeploy()
{
	date
	kubectl --context $hub_cluster_name delete -k https://github.com/$ocm_ramen_samples_git_path/ocm-ramen-samples/subscriptions?ref=$ocm_ramen_samples_git_ref
	date
}
exit_stack_push unset -f ramen_samples_channel_and_drpolicy_undeploy
application_sample_place()
{
	set -- $1 "$2" $3 $4 "$5" $6 ocm-ramen-samples subscriptions/busybox
	set -- $1 "$2" $3 https://$4/$ocm_ramen_samples_git_path/$7$5/$8$6 /tmp/$USER/$7/$8
	mkdir -p $5
	cat <<-a >$5/kustomization.yaml
	resources:
	  - $4
	namespace: busybox-sample
	patchesJson6902:
	  - target:
	      group: ramendr.openshift.io
	      version: v1alpha1
	      kind: DRPlacementControl
	      name: busybox-drpc
	    patch: |-
	      - op: add
	        path: /spec/action
	        value: $2
	      - op: add
	        path: /spec/$3Cluster
	        value: $1
	a
	kubectl create namespace busybox-sample --dry-run=client -o yaml | kubectl --context $1 apply -f -
	kubectl --context $hub_cluster_name apply -k $5
	until_true_or_n 90 eval test \"\$\(kubectl --context ${hub_cluster_name} -n busybox-sample get subscriptions/busybox-sub -ojsonpath='{.status.phase}'\)\" = Propagated
	until_true_or_n 30 eval test \"\$\(kubectl --context $hub_cluster_name -n busybox-sample get placementrules/busybox-placement -ojsonpath='{.status.decisions[].clusterName}'\)\" = $1
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
exit_stack_push unset -f application_sample_place
application_sample_undeploy_wait_and_namespace_undeploy()
{
	date
	true_if_exit_status_and_stderr 1 'error: no matching resources found' \
	kubectl --context ${1} -n busybox-sample wait pods/busybox --for delete --timeout 2m
	date
	true_if_exit_status_and_stderr 1 'error: no matching resources found' \
	kubectl --context ${1} -n busybox-sample wait volumereplicationgroups/busybox-drpc --for delete
	date
	true_if_exit_status_and_stderr 1 'error: no matching resources found' \
	kubectl --context $1 -n busybox-sample wait persistentvolumeclaims/busybox-pvc --for delete
	date
	kubectl --context $1 delete $2 namespace/busybox-sample
	date
}
exit_stack_push unset -f application_sample_undeploy_wait_and_namespace_undeploy
application_sample_deploy()
{
	set -- $spoke_cluster_names
	application_sample_place $1 '' preferred github.com '' \?ref=$ocm_ramen_samples_git_ref
}
exit_stack_push unset -f application_sample_deploy
application_sample_failover()
{
	set -- $spoke_cluster_names
	application_sample_place $2 Failover failover raw.githubusercontent.com /$ocm_ramen_samples_git_ref /drpc.yaml
	application_sample_undeploy_wait_and_namespace_undeploy $1
}
exit_stack_push unset -f application_sample_failover
application_sample_relocate()
{
	set -- $spoke_cluster_names
	application_sample_place $1 Relocate preferred raw.githubusercontent.com /$ocm_ramen_samples_git_ref /drpc.yaml
	application_sample_undeploy_wait_and_namespace_undeploy $2
}
exit_stack_push unset -f application_sample_relocate
application_sample_undeploy()
{
	set -- $(kubectl --context ${hub_cluster_name} -n busybox-sample get placementrules/busybox-placement -ojsonpath='{.status.decisions[].clusterName}')
	kubectl --context $hub_cluster_name delete -k https://github.com/$ocm_ramen_samples_git_path/ocm-ramen-samples/subscriptions/busybox?ref=$ocm_ramen_samples_git_ref
	application_sample_undeploy_wait_and_namespace_undeploy $1
}
exit_stack_push unset -f application_sample_undeploy
ramen_directory_path_name=${ramen_hack_directory_path_name}/..
exit_stack_push unset -v ramen_directory_path_name
hub_cluster_name=${hub_cluster_name:-hub}
exit_stack_push unset -v hub_cluster_name
spoke_cluster_names=${spoke_cluster_names:-cluster1\ $hub_cluster_name}
exit_stack_push unset -v spoke_cluster_names
rook_ceph_deploy()
{
	# volumes required: mirror sources, mirror targets, minio backend
	for cluster_name in $spoke_cluster_names; do
		rook_ceph_deploy_spoke $cluster_name
	done; unset -v cluster_name
	rook_ceph_mirrors_deploy $spoke_cluster_names
}
exit_stack_push unset -f rook_ceph_deploy
rook_ceph_undeploy()
{
	for cluster_name in $spoke_cluster_names; do
		rook_ceph_undeploy_spoke $cluster_name
	done; unset -v cluster_name
}
exit_stack_push unset -f rook_ceph_undeploy
rook_ceph_csi_image_canary_deploy()
{
	for cluster_name in $spoke_cluster_names; do
		minikube -p $cluster_name ssh -- docker image pull quay.io/cephcsi/cephcsi:canary
		kubectl --context $cluster_name -n rook-ceph rollout restart deploy/csi-rbdplugin-provisioner
	done; unset -v cluster_name
}
exit_stack_push unset -f rook_ceph_csi_image_canary_deploy
rook_ceph_volume_replication_image_latest_deploy()
{
	for cluster_name in $spoke_cluster_names; do
		minikube -p $cluster_name ssh -- docker image pull quay.io/csiaddons/volumereplication-operator:latest
		kubectl --context $cluster_name -n rook-ceph rollout restart deploy/csi-rbdplugin-provisioner
	done; unset -v cluster_name
}
exit_stack_push unset -f rook_ceph_volume_replication_image_latest_deploy
ramen_deploy()
{
	ramen_deploy_hub
	for cluster_name in $spoke_cluster_names; do ramen_deploy_spoke $cluster_name; done; unset -v cluster_name
}
exit_stack_push unset -f ramen_deploy
ramen_undeploy()
{
	for cluster_name in $spoke_cluster_names; do ramen_undeploy_spoke $cluster_name; done; unset -v cluster_name
	set +e # TODO remove once each resource is owned by hub or spoke but not both
	ramen_undeploy_hub
	set -e
}
exit_stack_push unset -f ramen_undeploy
deploy()
{
	hub_cluster_name=$hub_cluster_name spoke_cluster_names=$spoke_cluster_names $ramen_hack_directory_path_name/ocm-minikube.sh
	rook_ceph_deploy
	minio_deploy
# ENV variable to skip building ramen
#   - deploy will expect pre-loaded local docker image named
#     ${ramen_image_directory_name}/${ramen_image_name}:${ramen_image_tag}
	if test "${skip_ramen_build:-false}" = false; then
		ramen_build
	fi
	ramen_deploy
}
exit_stack_push unset -f deploy
undeploy()
{
	ramen_undeploy
	minio_undeploy
	rook_ceph_undeploy
}
exit_stack_push unset -f undeploy
exit_stack_push unset -v command
for command in "${@:-deploy}"; do
	$command
done
