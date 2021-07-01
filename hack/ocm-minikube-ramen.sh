#!/bin/sh
# shellcheck disable=1090,2046,2086
set -x
set -e
ramen_hack_directory_path_name=$(dirname $0)
. $ramen_hack_directory_path_name/exit_stack.sh
exit_stack_push unset -v ramen_hack_directory_path_name
rook_ceph_deploy()
{
	PROFILE=${2} ${1}/minikube-rook-setup.sh create
	PROFILE=${3} ${1}/minikube-rook-setup.sh create
	PRIMARY_CLUSTER=${2} SECONDARY_CLUSTER=${3} ${1}/minikube-rook-mirror-setup.sh
	PRIMARY_CLUSTER=${3} SECONDARY_CLUSTER=${2} ${1}/minikube-rook-mirror-setup.sh
	PRIMARY_CLUSTER=${2} SECONDARY_CLUSTER=${3} ${1}/minikube-rook-mirror-test.sh
	PRIMARY_CLUSTER=${3} SECONDARY_CLUSTER=${2} ${1}/minikube-rook-mirror-test.sh
}
exit_stack_push unset -f rook_ceph_deploy
rook_ceph_undeploy()
{
	PROFILE=${3} ${1}/minikube-rook-setup.sh delete
	PROFILE=${2} ${1}/minikube-rook-setup.sh delete
}
exit_stack_push unset -f rook_ceph_undeploy
minio_deploy()
{
	kubectl --context ${2} apply -f ${1}/minio-deployment.yaml
}
exit_stack_push unset -f minio_deploy
minio_undeploy()
{
	kubectl --context ${2} delete -f ${1}/minio-deployment.yaml
}
exit_stack_push unset -f minio_undeploy
ramen_image_directory_name=localhost
ramen_image_name=ramen-operator
ramen_image_tag=v0.N
ramen_image_name_colon_tag=${ramen_image_directory_name}/${ramen_image_name}:${ramen_image_tag}
exit_stack_push unset -v ramen_image_name_colon_tag ramen_image_tag ramen_image_name ramen_image_directory_name
ramen_build()
{
	${ramen_hack_directory_path_name}/docker-uninstall.sh ${HOME}/.local/bin
	. ${ramen_hack_directory_path_name}/podman-docker-install.sh
	. ${ramen_hack_directory_path_name}/go-install.sh; go_install ${HOME}/.local; unset -f go_install
	make -C ${1} docker-build IMG=${ramen_image_name_colon_tag}
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
	make -C ${1} deploy IMG=${ramen_image_name_colon_tag}
	kube_context_set_undo
	kubectl --context ${2} -n ramen-system wait deployments --all --for condition=available --timeout 60s
	kubectl --context ${hub_cluster_name} label managedclusters/${2} name=${2} --overwrite
	cat <<-a | kubectl --context ${2} apply -f -
	apiVersion: replication.storage.openshift.io/v1alpha1
	kind: VolumeReplicationClass
	metadata:
	  name: volume-rep-class
	spec:
	  provisioner: rook-ceph.rbd.csi.ceph.com
	  parameters:
	    replication.storage.openshift.io/replication-secret-name: rook-csi-rbd-provisioner
	    replication.storage.openshift.io/replication-secret-namespace: rook-ceph
	a
}
exit_stack_push unset -f ramen_deploy
ramen_undeploy()
{
	kubectl --context ${2} delete volumereplicationclass/volume-rep-class
	kubectl --context ${hub_cluster_name} label managedclusters/${2} name-
	kube_context_set ${2}
	make -C ${1} undeploy
	kube_context_set_undo
	set +e
	kubectl --context ${2} -n ramen-system wait deployments --all --for delete
	# error: no matching resources found
	set -e
}
exit_stack_push unset -f ramen_undeploy
ramen_samples_branch_checkout()
{
	set -- ocm-ramen-samples test ShyamsundarR
	set -- ocm-ramen-samples test hatfieldbrian
	set +e
	git clone https://github.com/RamenDR/${1}
	# fatal: destination path 'ocm-ramen-samples' already exists and is not an empty directory.
	set -e
	git --git-dir ${1}/.git --work-tree ${1} checkout main
	exit_stack_push git --git-dir ${1}/.git --work-tree ${1} checkout -
	branch_name=${3}/${2}
	git --git-dir ${1}/.git fetch https://github.com/${3}/${1} ${2}:${branch_name}
	exit_stack_pop
	git --git-dir ${1}/.git --work-tree ${1} checkout ${branch_name}
	exit_stack_push git --git-dir ${1}/.git --work-tree ${1} checkout -
	unset -v branch_name
}
exit_stack_push unset -f ramen_samples_branch_checkout
ramen_samples_branch_checkout_undo()
{
	exit_stack_pop
}
exit_stack_push unset -f ramen_samples_branch_checkout_undo
application_sample_namespace_and_s3_deploy()
{
	ramen_samples_branch_checkout
	kubectl create namespace busybox-sample --dry-run=client -o yaml | kubectl --context ${1} apply -f -
	kubectl --context ${1} apply -f ocm-ramen-samples/subscriptions/busybox/s3secret.yaml -n busybox-sample
	ramen_samples_branch_checkout_undo
}
exit_stack_push unset -f application_sample_namespace_and_s3_deploy
application_sample_namespace_and_s3_undeploy()
{
	ramen_samples_branch_checkout
	kubectl --context ${1} delete -f ocm-ramen-samples/subscriptions/busybox/s3secret.yaml -n busybox-sample
	date
	kubectl --context ${1} delete namespace busybox-sample
	date
	ramen_samples_branch_checkout_undo
}
exit_stack_push unset -f application_sample_namespace_and_s3_undeploy
application_sample_deploy()
{
	ramen_samples_branch_checkout
	kubectl --context ${hub_cluster_name} apply -k ocm-ramen-samples/subscriptions
	kubectl --context ${hub_cluster_name} -n ramen-samples get channels/ramen-gitops
	kubectl --context ${hub_cluster_name} apply -f ocm-ramen-samples/mw-pv.yaml
	mkdir -p ocm-ramen-samples/subscriptions/busybox-${USER}
	cat <<-a >ocm-ramen-samples/subscriptions/busybox-${USER}/kustomization.yaml
	---
	resources:
	  - ../busybox
	patchesJson6902:
	  - target:
	      group: ramendr.openshift.io
	      version: v1alpha1
	      kind: DRPlacementControl
	      name: busybox-drpc
	    patch: |-
	      - op: replace
	        path: /spec/s3Endpoint
	        value: $(minikube --profile=${hub_cluster_name} -n minio service --url minio)
	a
	kubectl --context ${hub_cluster_name} apply -k ocm-ramen-samples/subscriptions/busybox-${USER}
	ramen_samples_branch_checkout_undo
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
	ramen_samples_branch_checkout
	kubectl --context ${hub_cluster_name} delete -k ocm-ramen-samples/subscriptions/busybox-${USER}
	rm -r ocm-ramen-samples/subscriptions/busybox-${USER}
	date
	set +e
	kubectl --context ${1} -n busybox-sample wait pods/busybox --for delete --timeout 2m
	# error: no matching resources found
	set -e
	date
	# TODO drplacementcontrols finalizer delete volumereplicationgroup manifest work instead
	kubectl --context ${1} -n busybox-sample get volumereplicationgroups/busybox-drpc
	kubectl --context ${hub_cluster_name} -n ${1} delete manifestworks/busybox-drpc-busybox-sample-vrg-mw
	date
	set +e
	kubectl --context ${1} -n busybox-sample wait volumereplicationgroups/busybox-drpc --for delete
	# error: no matching resources found
	set -e
	date
	kubectl --context ${hub_cluster_name} delete -f ocm-ramen-samples/mw-pv.yaml
	date
	kubectl --context ${hub_cluster_name} delete -k ocm-ramen-samples/subscriptions
	date
	ramen_samples_branch_checkout_undo
}
exit_stack_push unset -f application_sample_undeploy
ramen_directory_path_name=${ramen_hack_directory_path_name}/..
exit_stack_push unset -v ramen_directory_path_name
hub_cluster_name=${hub_cluster_name:-hub}
exit_stack_push unset -v hub_cluster_name
spoke_cluster_names=${spoke_cluster_names:-${hub_cluster_name}\ cluster1}
exit_stack_push unset -v spoke_cluster_names
for cluster_name in ${spoke_cluster_names}; do
	if test ${cluster_name} = ${hub_cluster_name}; then
		spoke_cluster_names_hub=${spoke_cluster_names_hub}\ ${cluster_name}
	else
		spoke_cluster_names_nonhub=${spoke_cluster_names_nonhub}\ ${cluster_name}
	fi
done; unset -v cluster_name
cluster_names=${hub_cluster_name}\ ${spoke_cluster_names_nonhub}
exit_stack_push unset -v cluster_names
ramen_deploy_all()
{
	. ${ramen_hack_directory_path_name}/go-install.sh; go_install ${HOME}/.local; unset -f go_install
	for cluster_name in ${cluster_names}; do
		ramen_deploy ${ramen_directory_path_name} ${cluster_name}
	done; unset -v cluster_name
}
exit_stack_push unset -v ramen_deploy_all
ramen_undeploy_all()
{
	. ${ramen_hack_directory_path_name}/go-install.sh; go_install ${HOME}/.local; unset -f go_install
	for cluster_name in ${cluster_names}; do
		ramen_undeploy ${ramen_directory_path_name} ${cluster_name}
	done; unset -v cluster_name
}
exit_stack_push unset -v ramen_undeploy_all
exit_stack_push unset -v command
for command in "${@:-deploy}"; do
	case ${command} in
	deploy)
		hub_cluster_name=${hub_cluster_name} spoke_cluster_names=${spoke_cluster_names}\
		${ramen_hack_directory_path_name}/ocm-minikube.sh
		rook_ceph_deploy ${ramen_hack_directory_path_name} ${cluster_names}
		minio_deploy ${ramen_hack_directory_path_name} ${hub_cluster_name}
		ramen_build ${ramen_directory_path_name}
		ramen_archive
		ramen_deploy_all
		;;
	undeploy)
		ramen_undeploy_all
		minio_undeploy ${ramen_hack_directory_path_name} ${hub_cluster_name}
		rook_ceph_undeploy ${ramen_hack_directory_path_name} ${cluster_names}
		;;
	application_sample_deploy)
		for cluster_name in ${cluster_names}; do
			application_sample_namespace_and_s3_deploy ${cluster_name}
		done; unset -v cluster_name
		. ${ramen_hack_directory_path_name}/until_true_or_n.sh
		application_sample_deploy
		unset -f until_true_or_n
		;;
	application_sample_undeploy)
		application_sample_undeploy
		for cluster_name in ${cluster_names}; do
			application_sample_namespace_and_s3_undeploy ${cluster_name}
		done; unset -v cluster_name
		;;
	ramen_build)
		ramen_build ${ramen_directory_path_name}
		;;
	ramen_archive)
		ramen_archive
		;;
	ramen_deploy)
		ramen_deploy_all
		;;
	ramen_undeploy)
		ramen_undeploy_all
		;;
	rook_ceph_deploy)
		rook_ceph_deploy ${ramen_hack_directory_path_name} ${cluster_names}
		;;
	rook_ceph_undeploy)
		rook_ceph_undeploy ${ramen_hack_directory_path_name} ${cluster_names}
		;;
	*)
		echo subcommand unsupported: ${command}
		;;
	esac
done
git_branch_delete()
{
	set +e
	git --git-dir ${1}/.git branch -d ${2}
	# error: branch '${2}' not found.
	set -e
}
exit_stack_push unset -f git_branch_delete
git_branch_delete ${ramen_directory_path_name} pr47_pr58_rbac2360357
git_branch_delete ocm-ramen-samples shyam_test_benamar_update_placement_to_avr
