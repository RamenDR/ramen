#!/bin/sh
# shellcheck disable=1090,2046,2086
set -x
set -e
trap 'set -- ${?}; trap - EXIT; eval ${exit_stack}; echo exit status: ${1}' EXIT
trap 'trap - ABRT' ABRT
trap 'trap - QUIT' QUIT
trap 'trap - TERM' TERM
trap 'trap - INT' INT
trap 'trap - HUP' HUP
exit_stack_push()
{
	{ set +x; } 2>/dev/null
	exit_stack=${*}\;${exit_stack}
	set -x
}
exit_stack_push unset -v exit_stack
exit_stack_push unset -f exit_stack_push
exit_stack_pop()
{
	IFS=\; read -r x exit_stack <<-a
	${exit_stack}
	a
	eval ${x}
	unset -v x
}
exit_stack_push unset -f exit_stack_pop
rook_ceph_branch_checkout()
{
return
	git --git-dir ${1}/.git --work-tree ${1} checkout main
	exit_stack_push git --git-dir ${1}/.git --work-tree ${1} checkout -
	#git --git-dir ${1}/.git fetch https://github.com/ShyamsundarR/ramen rook-setup:rook-setup
	git --git-dir ${1}/.git fetch https://github.com/hatfieldbrian/ramen rook-setup:rook-setup
	exit_stack_pop
	git --git-dir ${1}/.git --work-tree ${1} checkout rook-setup
	exit_stack_push git --git-dir ${1}/.git --work-tree ${1} checkout -
}
exit_stack_push unset -f rook_ceph_branch_checkout
rook_ceph_branch_checkout_undo()
{
return
	exit_stack_pop
}
exit_stack_push unset -f rook_ceph_branch_checkout_undo
rook_ceph_deploy()
{
	rook_ceph_branch_checkout ${1}/..
	PROFILE=${2} ${1}/minikube-rook-setup.sh create
	PROFILE=${3} ${1}/minikube-rook-setup.sh create
	PRIMARY_CLUSTER=${2} SECONDARY_CLUSTER=${3} ${1}/minikube-rook-mirror-setup.sh
	PRIMARY_CLUSTER=${3} SECONDARY_CLUSTER=${2} ${1}/minikube-rook-mirror-setup.sh
	PRIMARY_CLUSTER=${2} SECONDARY_CLUSTER=${3} ${1}/minikube-rook-mirror-test.sh
	PRIMARY_CLUSTER=${3} SECONDARY_CLUSTER=${2} ${1}/minikube-rook-mirror-test.sh
	rook_ceph_branch_checkout_undo
}
exit_stack_push unset -f rook_ceph_deploy
rook_ceph_undeploy()
{
	rook_ceph_branch_checkout ${1}/..
	PROFILE=${3} ${1}/minikube-rook-setup.sh delete
	PROFILE=${2} ${1}/minikube-rook-setup.sh delete
	rook_ceph_branch_checkout_undo
}
exit_stack_push unset -f rook_ceph_undeploy
ramen_branch_name=pr47_pr58_rbac2360357
exit_stack_push unset -v ramen_branch_name
ramen_branch_build()
{
	set -- ${1} ${ramen_branch_name}
	git --git-dir ${1}/.git --work-tree ${1} checkout main -b ${2}
	exit_stack_push git --git-dir ${1}/.git --work-tree ${1} checkout -
	git --git-dir ${1}/.git --work-tree ${1} pull --rebase https://github.com/ramendr/ramen main
	#git --git-dir ${1}/.git --work-tree ${1} pull --rebase https://github.com/BenamarMk/ramen avr_per_subscription_placement       #51
	#git --git-dir ${1}/.git --work-tree ${1} pull --rebase https://github.com/ShyamsundarR/ramen pr-51-1
	#git --git-dir ${1}/.git --work-tree ${1} pull --rebase https://github.com/BenamarMk/ramen add_avr_plrule_to_manage_user_plrule #55
	git --git-dir ${1}/.git --work-tree ${1} pull --rebase https://github.com/ShyamsundarR/ramen 2360357b37e76f1bcd5909598d49c1756780cd8e
	git --git-dir ${1}/.git --work-tree ${1} pull --rebase https://github.com/BenamarMk/ramen add_preferredcluster_targetcluster    #58
	# controllers/applicationvolumereplication_controller.go
	# config/crd/bases/ramendr.openshift.io_applicationvolumereplications.yaml
	git --git-dir ${1}/.git --work-tree ${1} push git@github.com:hatfieldbrian/ramen ${2}
	exit_stack_pop
}
exit_stack_push unset -f ramen_branch_build
ramen_branch_checkout()
{
	set -- ${1} ${ramen_branch_name}
	git --git-dir ${1}/.git --work-tree ${1} checkout main
	exit_stack_push git --git-dir ${1}/.git --work-tree ${1} checkout -
	git --git-dir ${1}/.git fetch https://github.com/hatfieldbrian/ramen ${2}:${2}
	exit_stack_pop
	git --git-dir ${1}/.git --work-tree ${1} checkout ${2}
	exit_stack_push git --git-dir ${1}/.git --work-tree ${1} checkout -
}
exit_stack_push unset -f ramen_branch_checkout
ramen_branch_checkout_undo()
{
	exit_stack_pop
}
exit_stack_push unset -f ramen_branch_checkout_undo
ramen_image_name=ramen-operator:v0.Npr58
exit_stack_push unset -v ramen_image_name
ramen_build()
{
	ramen_branch_checkout ${1}
	make -C ${1} docker-build IMG=${ramen_image_name} DOCKER_HOST=${DOCKER_HOST}
	git --git-dir ${1}/.git --work-tree ${1} status -s
	git --git-dir ${1}/.git --work-tree ${1} checkout -- config
	ramen_branch_checkout_undo
}
exit_stack_push unset -f ramen_build
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
	DOCKER_HOST=${DOCKER_HOST} minikube -p ${2} image load ${ramen_image_name}
	ramen_branch_checkout ${1}
	kube_context_set ${2}
	make -C ${1} deploy IMG=${ramen_image_name}
	kube_context_set_undo
	git --git-dir ${1}/.git --work-tree ${1} status -s
	git --git-dir ${1}/.git --work-tree ${1} checkout -- config
	ramen_branch_checkout_undo
	kubectl --context ${2} -n ramen-system wait deployments --all --for condition=available
	kubectl --context ${hub_cluster_name} label managedclusters/${2} name=${2} --overwrite
}
exit_stack_push unset -f ramen_deploy
ramen_undeploy()
{
	kubectl --context ${hub_cluster_name} label managedclusters/${2} name-
	ramen_branch_checkout ${1}
	kube_context_set ${2}
	make -C ${1} undeploy
	kube_context_set_undo
	git --git-dir ${1}/.git --work-tree ${1} status -s
	git --git-dir ${1}/.git --work-tree ${1} checkout -- config
	ramen_branch_checkout_undo
	set +e
	kubectl --context ${2} -n ramen-system wait deployments --all --for delete
	# error: no matching resources found
	set -e
}
exit_stack_push unset -f ramen_undeploy
ocm_operator_build()
{
	set +e
	git clone https://github.com/ShyamsundarR/${1}
	# fatal: destination path '${1}' already exists and is not an empty directory.
	set -e
	git --git-dir ${1}/.git fetch https://github.com/ShyamsundarR/${1} pause-on-plchange:pause-on-plchange
	make -C ${1} build-images TRAVIS_BUILD=0 DOCKER_HOST=${DOCKER_HOST}
}
#ocm_operator_build multicloud-operators-deployable
#ocm_operator_build multicloud-operators-subscription
unset -f ocm_operator_build
ramen_samples_branch_name=shyam_test_benamar_update_placement_to_avr
exit_stack_push unset -v ramen_samples_branch_name
ramen_samples_branch_build()
{
	set -- ocm-ramen-samples ${ramen_samples_branch_name}
	set +e
	git clone https://github.com/RamenDR/${1}
	# fatal: destination path 'ocm-ramen-samples' already exists and is not an empty directory.
	set -e
	git --git-dir ${1}/.git --work-tree ${1} checkout main -b ${2}
	git --git-dir ${1}/.git --work-tree ${1} pull --ff-only https://github.com/ShyamsundarR/${1} test
	git --git-dir ${1}/.git --work-tree ${1} pull --rebase https://github.com/BenamarMk/${1} update_placement_to_avr
	git --git-dir ${1}/.git --work-tree ${1} push git@github.com:hatfieldbrian/${1} ${2}:${2}
	git --git-dir ${1}/.git --work-tree ${1} checkout -
}
exit_stack_push unset -f ramen_samples_branch_build
ramen_samples_branch_checkout()
{
	set -- ocm-ramen-samples ${ramen_samples_branch_name}
	set +e
	git clone https://github.com/RamenDR/${1}
	# fatal: destination path 'ocm-ramen-samples' already exists and is not an empty directory.
	set -e
	git --git-dir ${1}/.git --work-tree ${1} checkout main
	exit_stack_push git --git-dir ${1}/.git --work-tree ${1} checkout -
	git --git-dir ${1}/.git --work-tree ${1} fetch https://github.com/hatfieldbrian/${1} ${2}:${2}
	exit_stack_pop
	git --git-dir ${1}/.git --work-tree ${1} checkout ${2}
	exit_stack_push git --git-dir ${1}/.git --work-tree ${1} checkout -
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
	kubectl --context ${1} apply -f ocm-ramen-samples/subscriptions/busybox/namespace.yaml
	kubectl --context ${1} apply -f ocm-ramen-samples/subscriptions/busybox/s3secret.yaml
	ramen_samples_branch_checkout_undo
}
exit_stack_push unset -f application_sample_namespace_and_s3_deploy
application_sample_namespace_and_s3_undeploy()
{
	ramen_samples_branch_checkout
	kubectl --context ${1} delete -f ocm-ramen-samples/subscriptions/busybox/s3secret.yaml
	date
	kubectl --context ${1} delete -f ocm-ramen-samples/subscriptions/busybox/namespace.yaml
	date
	ramen_samples_branch_checkout_undo
}
exit_stack_push unset -f application_sample_namespace_and_s3_undeploy
application_sample_deploy()
{
	ramen_samples_branch_checkout
	kubectl --context ${hub_cluster_name} apply -f ocm-ramen-samples/minio-deployment.yaml
	kubectl --context ${hub_cluster_name} apply -k ocm-ramen-samples/subscriptions
	kubectl --context ${hub_cluster_name} -n ramen-samples get channels/ramen-gitops
	kubectl --context ${hub_cluster_name} apply -f ocm-ramen-samples/subscriptions/busybox/mw-pv.yaml
	mkdir -p ocm-ramen-samples/subscriptions/busybox-${USER}
	cat <<-a >ocm-ramen-samples/subscriptions/busybox-${USER}/kustomization.yaml
	resources:
	- ../busybox
	patchesJson6902:
	- target:
	    group: ramendr.openshift.io
	    version: v1alpha1
	    kind: ApplicationVolumeReplication
	    name: busybox-avr
	  patch: |-
	    - op: replace
	      path: /spec/s3Endpoint
	      value: $(minikube --profile=${hub_cluster_name} -n minio service --url minio)
	a
	kubectl --context ${hub_cluster_name} apply -k ocm-ramen-samples/subscriptions/busybox-${USER}
	ramen_samples_branch_checkout_undo
	kubectl --context ${hub_cluster_name} -n busybox-sample get placementrules/busybox-placement
	until_true_or_n 30 eval test \$\(kubectl --context ${hub_cluster_name} -n busybox-sample get subscriptions/busybox-sub -ojsonpath='{.status.phase}'\) = Propagated
	set -- $(kubectl --context ${hub_cluster_name} -n busybox-sample get placementrules/busybox-placement -ojsonpath='{.status.decisions[].clusterName}')
	if test ${1} = ${hub_cluster_name}; then
		subscription_name_suffix=-local
	else
		unset -v subscription_name_suffix
	fi
	until_true_or_n 30 eval test \$\(kubectl --context ${1} -n busybox-sample get subscriptions/busybox-sub${subscription_name_suffix} -ojsonpath='{.status.phase}'\) = Subscribed
	unset -v subscription_name_suffix
	until_true_or_n 60 kubectl --context ${1} -n busybox-sample wait pods/busybox --for condition=ready --timeout 0
	until_true_or_n 30 eval test \$\(kubectl --context ${1} -n busybox-sample get persistentvolumeclaims/busybox-pvc -ojsonpath='{.status.phase}'\) = Bound
	date
	until_true_or_n 90 kubectl --context ${1} -n busybox-sample get volumereplicationgroups/busybox-avr
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
	# TODO applicationvolumereplication finalizer delete volumereplicationgroup manifest work instead
	kubectl --context ${1} -n busybox-sample get volumereplicationgroups/busybox-avr
	kubectl --context ${hub_cluster_name} -n ${1} delete manifestworks/busybox-avr-busybox-sample-vrg-mw
	date
	set +e
	kubectl --context ${1} -n busybox-sample wait volumereplicationgroups/busybox-avr --for delete
	# error: no matching resources found
	set -e
	date
	kubectl --context ${hub_cluster_name} delete -k ocm-ramen-samples/subscriptions
	ramen_samples_branch_checkout_undo
}
exit_stack_push unset -f application_sample_undeploy
ramen_hack_directory_path_name=$(dirname ${0})
exit_stack_push unset -v ramen_hack_directory_path_name
ramen_directory_path_name=${ramen_hack_directory_path_name}/..
exit_stack_push unset -v ramen_directory_path_name
exit_stack_push unset -v command
hub_cluster_name=${hub_cluster_name:-hub}
exit_stack_push unset -v hub_cluster_name
spoke_cluster_names=${spoke_cluster_names:-${hub_cluster_name}\ cluster1}
exit_stack_push unset -v spoke_cluster_names
cluster_names=${hub_cluster_name}
exit_stack_push unset -v cluster_names
for cluster_name in ${spoke_cluster_names}; do
	if test ${cluster_name} != ${hub_cluster_name}; then
		cluster_names=${cluster_names}\ ${cluster_name}
	fi
done; unset -v cluster_name
for command in "${@:-deploy}"; do
	case ${command} in
	deploy)
		hub_cluster_name=${hub_cluster_name} spoke_cluster_names=${spoke_cluster_names}\
		${ramen_hack_directory_path_name}/ocm-minikube.sh
		rook_ceph_deploy ${ramen_hack_directory_path_name} ${cluster_names}
		. ${ramen_hack_directory_path_name}/docker-install.sh; docker_install ${HOME}/.local/bin; unset -f docker_install
		. ${ramen_hack_directory_path_name}/go-install.sh; go_install ${HOME}/.local; unset -f go_install
		${ramen_hack_directory_path_name}/kubectl-install.sh ${HOME}/.local/bin
		${ramen_hack_directory_path_name}/kustomize-install.sh ${HOME}/.local/bin
		ramen_build ${ramen_directory_path_name}
		for cluster_name in ${cluster_names}; do
			ramen_deploy ${ramen_directory_path_name} ${cluster_name}
		done; unset -v cluster_name
		unset -v DOCKER_HOST
		;;
	undeploy)
		. ${ramen_hack_directory_path_name}/go-install.sh; go_install ${HOME}/.local; unset -f go_install
		for cluster_name in ${cluster_names}; do
			ramen_undeploy ${ramen_directory_path_name} ${cluster_name}
		done; unset -v cluster_name
		rook_ceph_undeploy ${ramen_hack_directory_path_name} ${cluster_names}
		;;
	application_sample_deploy)
		for cluster_name in ${cluster_names}; do
			application_sample_namespace_and_s3_deploy ${cluster_name}
		done
		kubectl --context ${hub_cluster_name} label managedclusters/${cluster_name} region=west --overwrite
		unset -v cluster_name
		kubectl --context ${hub_cluster_name} get managedclusters --show-labels
		. ${ramen_hack_directory_path_name}/until_true_or_n.sh
		application_sample_deploy
		unset -f until_true_or_n
		;;
	application_sample_undeploy)
		application_sample_undeploy
		for cluster_name in ${cluster_names}; do
			application_sample_namespace_and_s3_undeploy ${cluster_name}
		done
		kubectl --context ${hub_cluster_name} label managedclusters/${cluster_name} region-
		unset -v cluster_name
		;;
	ramen_undeploy)
		. ${ramen_hack_directory_path_name}/go-install.sh; go_install ${HOME}/.local; unset -f go_install
		for cluster_name in ${cluster_names}; do
                        ramen_undeploy ${ramen_directory_path_name} ${cluster_name}
                done; unset -v cluster_name
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
