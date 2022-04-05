#!/bin/sh
# shellcheck disable=1090,2046,2086,1091
set -x
set -e
ramen_hack_directory_path_name=$(dirname $0)
. $ramen_hack_directory_path_name/exit_stack.sh
. $ramen_hack_directory_path_name/true_if_exit_status_and_stderr.sh
exit_stack_push unset -f true_if_exit_status_and_stderr
. $ramen_hack_directory_path_name/until_true_or_n.sh
exit_stack_push unset -f until_true_or_n
. $ramen_hack_directory_path_name/olm.sh
exit_stack_push olm_unset
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
	kubectl --context $1 apply -f $ramen_hack_directory_path_name/minio-deployment.yaml
	date
	kubectl --context $1 -n minio wait deployments/minio --for condition=available --timeout 60s
	date
}
exit_stack_push unset -f minio_deploy
minio_undeploy()
{
	kubectl --context $1 delete -f $ramen_hack_directory_path_name/minio-deployment.yaml
}
exit_stack_push unset -f minio_undeploy
minio_deploy_spokes()
{
	for cluster_name in $spoke_cluster_names; do minio_deploy $cluster_name; done; unset -v cluster_name
}
exit_stack_push unset -f minio_deploy_spokes
minio_undeploy_spokes()
{
	for cluster_name in $spoke_cluster_names; do minio_undeploy $cluster_name; done; unset -v cluster_name
}
exit_stack_push unset -f minio_undeploy_spokes
image_registry_port_number=5000
exit_stack_push unset -v image_registry_port_number
image_registry_address=localhost:$image_registry_port_number
exit_stack_push unset -v image_registry_address
image_registry_deployment_name=myregistry
exit_stack_push unset -v image_registry_deployment_name
image_registry_container_image_reference=docker.io/library/registry:2
exit_stack_push unset -v image_registry_container_image_reference
image_registry_container_deploy_command="docker run -d --name $image_registry_deployment_name -p $image_registry_port_number:$image_registry_port_number $image_registry_container_image_reference"
exit_stack_push unset -v image_registry_container_deploy_command
image_registry_container_undeploy_command="docker container stop $image_registry_deployment_name;docker container rm -v $image_registry_deployment_name"
exit_stack_push unset -v image_registry_container_undeploy_command
image_registry_container_deploy_localhost()
{
	$image_registry_container_deploy_command
}
exit_stack_push unset -f image_registry_container_deploy_localhost
image_registry_container_undeploy_localhost()
{
	eval $image_registry_container_undeploy_command
}
exit_stack_push unset -f image_registry_container_undeploy_localhost
image_registry_container_deploy_cluster()
{
	minikube -p $1 ssh -- "$image_registry_container_deploy_command"
}
exit_stack_push unset -f image_registry_container_deploy_cluster
image_registry_container_undeploy_cluster()
{
	minikube -p $1 ssh -- "$image_registry_container_undeploy_command"
}
exit_stack_push unset -f image_registry_container_undeploy_cluster
image_registry_addon_deploy_cluster()
{
	minikube -p $1 addons enable registry
	date
	kubectl --context $1 -n kube-system -l kubernetes.io/minikube-addons=registry wait --for condition=ready pods
	date
	# Get http://localhost:5000/v2/: read tcp 127.0.0.1:36378->127.0.0.1:5000: read: connection reset by peer
	until_true_or_n 30 minikube -p $1 ssh -- curl http://$image_registry_address/v2/
}
exit_stack_push unset -f image_registry_addon_deploy_cluster
image_registry_addon_undeploy_cluster()
{
	minikube -p $1 addons disable registry
	date
	kubectl --context $1 -n kube-system -l kubernetes.io/minikube-addons=registry wait --for delete all --timeout 2m
	date
}
exit_stack_push unset -f image_registry_addon_undeploy_cluster
image_registry_deployment_deploy_cluster()
{
	kubectl create --dry-run=client -o yaml deployment $image_registry_deployment_name --image $image_registry_container_image_reference --port $image_registry_port_number|kubectl --context $1 apply -f -
	kubectl --context $1 wait deployment/$image_registry_deployment_name --for condition=available
}
exit_stack_push unset -f image_registry_deployment_deploy_cluster
image_registry_deployment_address()
{
	kubectl --context $1 get $(kubectl --context $1 get pod -l app=$image_registry_deployment_name -o name) --template='{{.status.podIP}}':$image_registry_port_number
}
exit_stack_push unset -f image_registry_deployment_address
image_registry_deployment_undeploy_cluster()
{
	kubectl --context $1 delete deployment/$image_registry_deployment_name
}
exit_stack_push unset -f image_registry_deployment_undeploy_cluster
image_registry_deploy_cluster_method=addon
exit_stack_push unset -v image_registry_deploy_cluster_method
image_registry_deploy_cluster()
{
	image_registry_${image_registry_deploy_cluster_method}_deploy_cluster $1
}
exit_stack_push unset -f image_registry_deploy_cluster
image_registry_undeploy_cluster()
{
	image_registry_${image_registry_deploy_cluster_method}_undeploy_cluster $1
}
exit_stack_push unset -f image_registry_undeploy_cluster
image_registry_deploy_spokes()
{
	for cluster_name in $spoke_cluster_names; do image_registry_deploy_cluster $cluster_name; done; unset -v cluster_name
}
exit_stack_push unset -f image_registry_deploy_spokes
image_registry_undeploy_spokes()
{
	for cluster_name in $spoke_cluster_names; do image_registry_undeploy_cluster $cluster_name; done; unset -v cluster_name
}
exit_stack_push unset -f image_registry_undeploy_spokes
image_archive()
{
	set -- $1 $(echo $1|tr : _)
	set -- $1 $HOME/.minikube/cache/images/$(dirname $2) $(basename $2)
	mkdir -p $2
	set -- $1 $2/$3
	# docker-archive doesn't support modifying existing images
	rm -f $2
	docker image save $1 -o $2
}
exit_stack_push unset -f image_archive
image_load_cluster()
{
	minikube -p $1 image load $2
}
exit_stack_push unset -f image_load_cluster
image_and_containers_exited_using_remove_cluster()
{
	minikube -p $1 ssh -- docker container rm \$\(docker container ls --all --filter ancestor=$2 --filter status=exited --quiet\)\;docker image rm $2
}
exit_stack_push unset -f image_and_containers_exited_using_remove_cluster
image_remove_cluster()
{
	minikube -p $1 ssh -- docker image rm $2
}
exit_stack_push unset -f image_remove_cluster
image_push_cluster()
{
	minikube -p $1 ssh -- docker image push $2
}
exit_stack_push unset -f image_push_cluster
ramen_image_directory_name=${ramen_image_directory_name-ramendr}
exit_stack_push unset -v ramen_image_directory_name
ramen_image_name_prefix=ramen
exit_stack_push unset -v ramen_image_name_prefix
ramen_image_tag=${ramen_image_tag-canary}
exit_stack_push unset -v ramen_image_tag
ramen_image_reference()
{
	echo ${1:+$1/}${ramen_image_directory_name:+$ramen_image_directory_name/}$ramen_image_name_prefix-$2:$ramen_image_tag
}
exit_stack_push unset -f ramen_image_reference
ramen_image_reference_registry_local()
{
	ramen_image_reference $image_registry_address $1
}
exit_stack_push unset -f ramen_image_reference_registry_local
ramen_manager_image_reference=$(ramen_image_reference "${ramen_manager_image_registry_address-localhost}" operator)
exit_stack_push unset -v ramen_manager_image_reference
ramen_manager_image_build()
{
# ENV variable to skip building ramen
#   - expects docker image named:
#     [$ramen_manager_image_registry_address/][$ramen_image_directory_name/]ramen-operator:$ramen_image_tag
	if test "${skip_ramen_build:-false}" != false; then
		return
	fi
	${ramen_hack_directory_path_name}/docker-uninstall.sh ${HOME}/.local/bin
	. ${ramen_hack_directory_path_name}/podman-docker-install.sh
	. ${ramen_hack_directory_path_name}/go-install.sh; go_install ${HOME}/.local; unset -f go_install
	make -C $ramen_directory_path_name docker-build IMG=$ramen_manager_image_reference
}
exit_stack_push unset -f ramen_manager_image_build
ramen_manager_image_archive()
{
	image_archive $ramen_manager_image_reference
}
exit_stack_push unset -f ramen_manager_image_archive
ramen_manager_image_load_cluster()
{
	image_load_cluster $1 $ramen_manager_image_reference
}
exit_stack_push unset -f ramen_manager_image_load_cluster
ramen_manager_image_remove_cluster()
{
	image_remove_cluster $1 $ramen_manager_image_reference
}
exit_stack_push unset -f ramen_manager_image_remove_cluster
ramen_bundle_image_reference()
{
	ramen_image_reference_registry_local $1-operator-bundle
}
exit_stack_push unset -f ramen_bundle_image_reference
ramen_bundle_image_spoke_reference=$(ramen_bundle_image_reference dr-cluster)
exit_stack_push unset -v ramen_bundle_image_spoke_reference
ramen_bundle_image_build()
{
	make -C $ramen_directory_path_name bundle-$1-build\
		IMG=$ramen_manager_image_reference\
		BUNDLE_IMG_DRCLUSTER=$ramen_bundle_image_spoke_reference\
		IMAGE_TAG=$ramen_image_tag\

}
exit_stack_push unset -f ramen_bundle_image_build
ramen_bundle_image_spoke_build()
{
	ramen_bundle_image_build dr-cluster
}
exit_stack_push unset -f ramen_bundle_image_spoke_build
ramen_bundle_image_spoke_push()
{
	podman push --tls-verify=false $ramen_bundle_image_spoke_reference
}
exit_stack_push unset -f ramen_bundle_image_spoke_push
ramen_bundle_image_spoke_archive()
{
	image_archive $ramen_bundle_image_spoke_reference
}
exit_stack_push unset -f ramen_bundle_image_spoke_archive
ramen_bundle_image_spoke_load_cluster()
{
	image_load_cluster $1 $ramen_bundle_image_spoke_reference
}
exit_stack_push unset -f ramen_bundle_image_spoke_load_cluster
ramen_bundle_image_spoke_remove_cluster()
{
	image_and_containers_exited_using_remove_cluster $1 $ramen_bundle_image_spoke_reference
}
exit_stack_push unset -f ramen_bundle_image_spoke_remove_cluster
ramen_bundle_image_spoke_push_cluster()
{
	image_push_cluster $1 $ramen_bundle_image_spoke_reference
}
exit_stack_push unset -f ramen_bundle_image_spoke_push_cluster
ramen_catalog_image_reference=$(ramen_image_reference_registry_local operator-catalog)
exit_stack_push unset -v ramen_catalog_image_reference
ramen_catalog_image_build()
{
	make -C $ramen_directory_path_name catalog-build\
		BUNDLE_IMGS=$1\
		BUNDLE_PULL_TOOL=none\ --skip-tls\
		CATALOG_IMG=$ramen_catalog_image_reference\

}
exit_stack_push unset -f ramen_catalog_image_build
ramen_catalog_image_spoke_build()
{
	ramen_catalog_image_build $ramen_bundle_image_spoke_reference
}
exit_stack_push unset -f ramen_catalog_image_spoke_build
ramen_catalog_image_archive()
{
	image_archive $ramen_catalog_image_reference
}
exit_stack_push unset -f ramen_catalog_image_archive
ramen_catalog_image_load_cluster()
{
	image_load_cluster $1 $ramen_catalog_image_reference
}
exit_stack_push unset -f ramen_catalog_image_load_cluster
ramen_catalog_image_remove_cluster()
{
	image_remove_cluster $1 $ramen_catalog_image_reference
}
exit_stack_push unset -f ramen_catalog_image_remove_cluster
ramen_catalog_image_push_cluster()
{
	image_push_cluster $1 $ramen_catalog_image_reference
}
exit_stack_push unset -f ramen_catalog_image_push_cluster
ramen_images_build()
{
	ramen_manager_image_build
	ramen_bundle_image_spoke_build
	image_registry_container_deploy_localhost
	exit_stack_push image_registry_container_undeploy_localhost
	ramen_bundle_image_spoke_push
	ramen_catalog_image_spoke_build
	exit_stack_pop
}
exit_stack_push unset -f ramen_images_build
ramen_images_archive()
{
	ramen_manager_image_archive
	ramen_bundle_image_spoke_archive
	ramen_catalog_image_archive
}
exit_stack_push unset -f ramen_images_archive
ramen_images_build_and_archive()
{
	ramen_images_build
	ramen_images_archive
}
exit_stack_push unset -f ramen_images_build_and_archive
ramen_images_load_spoke()
{
	ramen_manager_image_load_cluster $1
	ramen_bundle_image_spoke_load_cluster $1
	ramen_catalog_image_load_cluster $1
}
exit_stack_push unset -f ramen_images_load_spoke
ramen_images_push_spoke()
{
	ramen_bundle_image_spoke_push_cluster $1
	ramen_catalog_image_push_cluster $1
}
exit_stack_push unset -f ramen_images_push_spoke
ramen_images_deploy_spoke()
{
	ramen_images_load_spoke	$1
	image_registry_deploy_cluster $1
	ramen_images_push_spoke	$1
}
exit_stack_push unset -f ramen_images_deploy_spoke
ramen_images_undeploy_spoke_common()
{
	image_registry_undeploy_cluster $1
	ramen_catalog_image_remove_cluster $1
	ramen_bundle_image_spoke_remove_cluster $1
}
exit_stack_push unset -f ramen_images_undeploy_spoke_common
ramen_images_undeploy_spoke_nonhub()
{
	ramen_images_undeploy_spoke_common $1
	ramen_manager_image_remove_cluster $1
}
exit_stack_push unset -f ramen_images_undeploy_spoke_nonhub
ramen_images_undeploy_spoke_hub()
{
	ramen_images_undeploy_spoke_common $1
}
exit_stack_push unset -f ramen_images_undeploy_spoke_hub
ramen_images_deploy_spokes()
{
	for cluster_name in $spoke_cluster_names; do ramen_images_deploy_spoke $cluster_name; done; unset -v cluster_name
}
exit_stack_push unset -f ramen_images_deploy_spokes
ramen_images_undeploy_spokes()
{
	for cluster_name in $spoke_cluster_names; do ramen_images_undeploy_spoke $cluster_name; done; unset -v cluster_name
}
exit_stack_push unset -f ramen_images_undeploy_spokes
ramen_catalog_kubectl()
{
	cat <<-a|kubectl --context $1 $2 -f -
	kind: CatalogSource
	apiVersion: operators.coreos.com/v1alpha1
	metadata:
	  name: ramen-catalog
	  namespace: ramen-system
	spec:
	  sourceType: grpc
	  image: $ramen_catalog_image_reference
	  displayName: "Ramen Operators"
	a
}
exit_stack_push unset -f ramen_catalog_deploy_cluster
ramen_catalog_deploy_cluster()
{
	ramen_catalog_kubectl $1 apply
	until_true_or_n 30 eval test \"\$\(kubectl --context $1 -n ramen-system get catalogsources.operators.coreos.com/ramen-catalog -ojsonpath='{.status.connectionState.lastObservedState}'\)\" = READY
}
exit_stack_push unset -f ramen_catalog_deploy_cluster
ramen_catalog_undeploy_cluster()
{
	ramen_catalog_kubectl $1 delete
	true_if_exit_status_and_stderr 1 'error: no matching resources found' \
	kubectl --context $1 -n ramen-system wait catalogsources.operators.coreos.com/ramen-catalog --for delete
}
exit_stack_push unset -f ramen_catalog_undeploy_cluster
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
	ramen_manager_image_load_cluster $1
	. $ramen_hack_directory_path_name/go-install.sh; go_install $HOME/.local; unset -f go_install
	kube_context_set $1
	make -C $ramen_directory_path_name deploy-$2 IMG=$ramen_manager_image_reference
	kube_context_set_undo
	kubectl --context $1 -n ramen-system wait deployments --all --for condition=available --timeout 2m
	ramen_config_deploy_hub_or_spoke $1 $2
}
exit_stack_push unset -f ramen_deploy_hub_or_spoke
ramen_s3_secret_kubectl_cluster()
{
	cat <<-EOF | kubectl --context $1 $2 -f -
	apiVersion: v1
	kind: Secret
	metadata:
	  name: s3secret
	  namespace: ramen-system
	stringData:
	  AWS_ACCESS_KEY_ID: minio
	  AWS_SECRET_ACCESS_KEY: minio123
	EOF
}
exit_stack_push unset -f ramen_s3_secret_kubectl_cluster
ramen_s3_secret_deploy_cluster()
{
	ramen_s3_secret_kubectl_cluster $1 apply
}
exit_stack_push unset -f ramen_s3_secret_deploy_cluster
ramen_s3_secret_undeploy_cluster()
{
	ramen_s3_secret_kubectl_cluster $1 delete\ "$2"
}
exit_stack_push unset -f ramen_s3_secret_undeploy_cluster
ramen_s3_secret_distribution_enabled=${ramen_s3_secret_distribution_enabled-true}
exit_stack_push unset -v ramen_s3_secret_distribution_enabled
ramen_s3_secret_deploy_cluster_wait()
{
	until_true_or_n 30 kubectl --context $1 -n ramen-system get secret/s3secret
}
exit_stack_push unset -f ramen_s3_secret_deploy_cluster_wait
ramen_s3_secret_undeploy_cluster_wait()
{
	true_if_exit_status_and_stderr 1 'error: no matching resources found' \
	kubectl --context $1 -n ramen-system wait secret/s3secret --for delete
}
exit_stack_push unset -f ramen_s3_secret_undeploy_cluster_wait
if test ramen_s3_secret_distribution_enabled = true; then
	secret_function_name_suffix=_wait
else
	secret_function_name_suffix=
fi
exit_stack_push unset -v secret_function_name_suffix
ramen_config_map_name()
{
	echo ramen-$1-operator-config
}
exit_stack_push unset -f ramen_config_map_name
ramen_config_file_path_name()
{
	echo $ramen_directory_path_name/config/$1/manager/ramen_manager_config.yaml
}
exit_stack_push unset -f ramen_config_file_path_name
ramen_config_replace_hub_or_spoke()
{
	kubectl create configmap $(ramen_config_map_name $2) --from-file=$3 -o yaml --dry-run=client |\
		kubectl --context $1 -n ramen-system replace -f -
}
exit_stack_push unset -f ramen_config_replace_hub_or_spoke
ramen_config_deploy_hub_or_spoke()
{
	ramen_s3_secret_deploy_cluster $1
	until_true_or_n 90 kubectl --context $1 -n ramen-system get configmap $(ramen_config_map_name $2)
	set -- $1 $2 /tmp/$USER/ramen/$2
	mkdir -p $3
	set -- $1 $2 $3/ramen_manager_config.yaml $spoke_cluster_names
	cat $(ramen_config_file_path_name $2) - <<-EOF >$3
	s3StoreProfiles:
	- s3ProfileName: minio-on-$4
	  s3Bucket: bucket
	  s3CompatibleEndpoint: $(minikube --profile $4 -n minio service --url minio)
	  s3Region: us-east-1
	  s3SecretRef:
	    name: s3secret
	    namespace: ramen-system
	- s3ProfileName: minio-on-$5
	  s3Bucket: bucket
	  s3CompatibleEndpoint: $(minikube --profile $5 -n minio service --url minio)
	  s3Region: us-west-1
	  s3SecretRef:
	    name: s3secret
	    namespace: ramen-system
	drClusterOperator:
	  deploymentAutomationEnabled: true
	  s3SecretDistributionEnabled: $ramen_s3_secret_distribution_enabled
	EOF
	ramen_config_replace_hub_or_spoke $1 $2 $3
}
exit_stack_push unset -f ramen_config_deploy_hub_or_spoke
ramen_config_undeploy_hub_or_spoke()
{
	ramen_config_replace_hub_or_spoke $1 $2 $(ramen_config_file_path_name $2)
	ramen_s3_secret_undeploy_cluster $1
}
exit_stack_push unset -f ramen_config_undeploy_hub_or_spoke
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
	ramen_config_undeploy_hub_or_spoke $1 $2
	kube_context_set $1
	make -C $ramen_directory_path_name undeploy-$2
	kube_context_set_undo
	ramen_manager_image_remove_cluster $1
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
ramen_deploy_spokes()
{
	for cluster_name in $spoke_cluster_names; do ramen_deploy_spoke $cluster_name; done; unset -v cluster_name
}
exit_stack_push unset -f ramen_deploy_spokes
ramen_undeploy_spokes()
{
	for cluster_name in $spoke_cluster_names; do ramen_undeploy_spoke $cluster_name; done; unset -v cluster_name
}
exit_stack_push unset -f ramen_undeploy_spokes
olm_deploy_spokes()
{
	for cluster_name in $spoke_cluster_names; do olm_deploy $cluster_name; done; unset -v cluster_name
}
exit_stack_push unset -f olm_deploy_spokes
olm_undeploy_spokes()
{
	for cluster_name in $spoke_cluster_names; do olm_undeploy $cluster_name; done; unset -v cluster_name
}
exit_stack_push unset -f olm_undeploy_spokes
ocm_ramen_samples_git_ref=${ocm_ramen_samples_git_ref-main}
ocm_ramen_samples_git_path=${ocm_ramen_samples_git_path-ramendr}
exit_stack_push unset -v ocm_ramen_samples_git_ref
exit_stack_push unset -v ocm_ramen_samples_git_path
ramen_samples_channel_and_drpolicy_deploy()
{
	for cluster_name in $spoke_cluster_names; do
		ramen_images_deploy_spoke $cluster_name
	done; unset -v cluster_name
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
	      kind: DRCluster
	      name: hub
	    patch: |-
	      - op: add
	        path: /spec/region
	        value: $4
	      - op: replace
	        path: /spec/s3ProfileName
	        value: minio-on-$4
	      - op: replace
	        path: /metadata/name
	        value: $4
	  - target:
	      group: ramendr.openshift.io
	      version: v1alpha1
	      kind: DRCluster
	      name: cluster1
	    patch: |-
	      - op: add
	        path: /spec/region
	        value: $3
	      - op: replace
	        path: /spec/s3ProfileName
	        value: minio-on-$3
	      - op: replace
	        path: /metadata/name
	        value: $3
	  - target:
	      group: ramendr.openshift.io
	      version: v1alpha1
	      kind: DRPolicy
	      name: dr-policy
	    patch: |-
	      - op: replace
	        path: /spec/drClusters
	        value:
	          - $3
	          - $4
	a
	kubectl --context $hub_cluster_name apply -k $1
	for cluster_name in $spoke_cluster_names; do
		until_true_or_n 300 kubectl --context $cluster_name get namespaces/ramen-system
		ramen_catalog_deploy_cluster $cluster_name
		until_true_or_n 300 kubectl --context $cluster_name -n ramen-system wait deployments ramen-dr-cluster-operator --for condition=available --timeout 0
		ramen_s3_secret_deploy_cluster$secret_function_name_suffix $cluster_name
	done; unset -v cluster_name
	kubectl --context $hub_cluster_name -n ramen-samples get channels/ramen-gitops
}
exit_stack_push unset -f ramen_samples_channel_and_drpolicy_deploy
ramen_samples_channel_and_drpolicy_undeploy()
{
	set --
	for cluster_name in $spoke_cluster_names; do
#spec.startingCSV\
		set -- $# "$@" $(kubectl --context $cluster_name -n ramen-system get subscriptions.operators.coreos.com/ramen-dr-cluster-subscription -ojsonpath=\{.\
status.installedCSV\
\}); test $(($1+2)) -eq $#; shift
		ramen_catalog_undeploy_cluster $cluster_name
	done; unset -v cluster_name
	date
	kubectl --context $hub_cluster_name delete -k https://github.com/$ocm_ramen_samples_git_path/ocm-ramen-samples/subscriptions?ref=$ocm_ramen_samples_git_ref
	date
	for cluster_name in $spoke_cluster_names; do
		date
		kubectl --context $cluster_name -n ramen-system delete clusterserviceversions.operators.coreos.com/$1 --ignore-not-found
		shift
		date
		true_if_exit_status_and_stderr 1 'error: no matching resources found' \
		kubectl --context $cluster_name -n ramen-system wait deployments ramen-dr-cluster-operator --for delete
		date
		# TODO remove once drpolicy controller does this
		kubectl --context $cluster_name delete customresourcedefinitions.apiextensions.k8s.io/volumereplicationgroups.ramendr.openshift.io
		date
	done; unset -v cluster_name
	for cluster_name in $spoke_cluster_names_nonhub; do
		date
		ramen_s3_secret_undeploy_cluster$secret_function_name_suffix $cluster_name --ignore-not-found
		date
		true_if_exit_status_and_stderr 1 'error: no matching resources found' \
		kubectl --context $cluster_name wait namespaces/ramen-system --for delete --timeout 2m
		date
		ramen_images_undeploy_spoke_nonhub $cluster_name
	done; unset -v cluster_name
	for cluster_name in $spoke_cluster_names_hub; do
		ramen_images_undeploy_spoke_hub $cluster_name
	done; unset -v cluster_name
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
	kubectl --context $hub_cluster_name apply -k $5
	until_true_or_n 90 eval test \"\$\(kubectl --context ${hub_cluster_name} -n busybox-sample get subscriptions/busybox-sub -ojsonpath='{.status.phase}'\)\" = Propagated
	until_true_or_n 120 eval test \"\$\(kubectl --context $hub_cluster_name -n busybox-sample get placementrules/busybox-placement -ojsonpath='{.status.decisions[].clusterName}'\)\" = $1
	if test ${1} = ${hub_cluster_name}; then
		subscription_name_suffix=-local
	else
		unset -v subscription_name_suffix
	fi
	until_true_or_n 30 eval test \"\$\(kubectl --context ${1} -n busybox-sample get subscriptions/busybox-sub${subscription_name_suffix} -ojsonpath='{.status.phase}'\)\" = Subscribed
	unset -v subscription_name_suffix
	until_true_or_n 120 kubectl --context $1 -n busybox-sample wait pods/busybox --for condition=ready --timeout 0
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
	kubectl --context $1 -n busybox-sample wait volumereplicationgroups/busybox-drpc --for delete --timeout 2m
	date
	true_if_exit_status_and_stderr 1 'error: no matching resources found' \
	kubectl --context $1 -n busybox-sample wait persistentvolumeclaims/busybox-pvc --for delete --timeout 2m
	# TODO remove once drplacement controller does this
	kubectl --context $hub_cluster_name -n $1 delete manifestworks/busybox-drpc-busybox-sample-ns-mw #--ignore-not-found
	true_if_exit_status_and_stderr 1 'error: no matching resources found' \
	kubectl --context $1 wait namespace/busybox-sample --for delete
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
for cluster_name in $spoke_cluster_names; do
	if test $cluster_name = $hub_cluster_name; then
		spoke_cluster_names_hub=$spoke_cluster_names_hub\ $cluster_name
	else
		spoke_cluster_names_nonhub=$spoke_cluster_names_nonhub\ $cluster_name
	fi
done; unset -v cluster_name
exit_stack_push unset -v spoke_cluster_names_hub
exit_stack_push unset -v spoke_cluster_names_nonhub
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
}
exit_stack_push unset -f ramen_deploy
ramen_undeploy()
{
	ramen_undeploy_hub
}
exit_stack_push unset -f ramen_undeploy
deploy()
{
	hub_cluster_name=$hub_cluster_name spoke_cluster_names=$spoke_cluster_names $ramen_hack_directory_path_name/ocm-minikube.sh
	rook_ceph_deploy
	minio_deploy_spokes
	ramen_images_build_and_archive
	olm_deploy_spokes
	ramen_deploy
}
exit_stack_push unset -f deploy
undeploy()
{
	ramen_undeploy
	olm_undeploy_spokes
	minio_undeploy_spokes
	rook_ceph_undeploy
}
exit_stack_push unset -f undeploy
exit_stack_push unset -v command
for command in "${@:-deploy}"; do
	$command
done
