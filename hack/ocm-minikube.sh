#!/bin/sh
# shellcheck disable=1091,2046,2086

# open cluster management (ocm) hub and managed minikube kvm amd64 clusters deploy
# https://github.com/ShyamsundarR/ocm-minikube/README.md

set -x
set -e
trap 'echo exit value: $?;trap - EXIT' EXIT

mkdir -p ${HOME}/.local/bin
PATH=${HOME}/.local/bin:${PATH}

ramen_hack_directory_path_name=$(dirname ${0})
${ramen_hack_directory_path_name}/minikube-install.sh ${HOME}/.local/bin
# 1.11 wait support
# 1.19 certificates.k8s.io/v1 https://github.com/kubernetes/kubernetes/pull/91685
${ramen_hack_directory_path_name}/kubectl-install.sh ${HOME}/.local/bin 1 19
${ramen_hack_directory_path_name}/kustomize-install.sh ${HOME}/.local/bin
# shellcheck source=./until_true_or_n.sh
. ${ramen_hack_directory_path_name}/until_true_or_n.sh
# TODO registration-operator go version minimum determine programatically
# shellcheck source=./go-install.sh
. ${ramen_hack_directory_path_name}/go-install.sh; go_install ${HOME}/.local 1.15.2; unset -f go_install
unset -v ramen_hack_directory_path_name

minikube_start_options=--driver=kvm2
. /etc/os-release # NAME

if ! command -v virsh; then
	# https://minikube.sigs.k8s.io/docs/drivers/kvm2/
	case ${NAME} in
	"Red Hat Enterprise Linux Server")
		# https://access.redhat.com/articles/1344173#Q_how-install-virtualization-packages
		sudo yum install libvirt -y
		;;
	"Ubuntu")
		# https://help.ubuntu.com/community/KVM/Installation
		sudo apt-get update
		if false # test ${VERSION_ID} -ge "18.10"
		then
			sudo apt-get install qemu-kvm libvirt-daemon-system libvirt-clients bridge-utils -y
		else
			sudo apt-get install qemu-kvm libvirt-bin ubuntu-vm-builder bridge-utils -y
		fi
		# shellcheck disable=SC2012
		sudo adduser ${LOGNAME} $(ls -l /var/run/libvirt/libvirt-sock|cut -d\  -f4)
		echo 'relogin for permission to access /var/run/libvirt/libvirt-sock, then rerun'
		false; exit
		;;
	esac
fi

ocm_minikube_checkout()
{
	set +e
	git clone https://github.com/ShyamsundarR/ocm-minikube
	# fatal: destination path 'ocm-minikube' already exists and is not an empty directory.
	set -e
	git --git-dir ocm-minikube/.git --work-tree ocm-minikube checkout main
	git --git-dir ocm-minikube/.git fetch https://github.com/ShyamsundarR/ocm-minikube main:shyam-main
	git --git-dir ocm-minikube/.git --work-tree ocm-minikube checkout -
	git --git-dir ocm-minikube/.git --work-tree ocm-minikube checkout shyam-main
}
ocm_registration_operator_checkout()
{
	set -- registration-operator
	set +e
	git clone https://github.com/open-cluster-management/${1}
	# fatal: destination path 'registration-operator' already exists and is not an empty directory.
	set -e
	git --git-dir ${1}/.git --work-tree ${1} checkout release-2.3
	# managed cluster names other than cluster1 require
	git --git-dir ${1}/.git --work-tree ${1} reset --hard f3b0287
	git apply --directory ${1} ocm-minikube/${1}.diff
}
case ${NAME} in
"Ubuntu")
	ocm_registration_operator_make_arguments=GO_REQUIRED_MIN_VERSION:=
	;;
esac
ocm_registration_operator_deploy_hub()
{
	kubectl --context ${hub_cluster_name} apply -f https://raw.githubusercontent.com/kubernetes/cluster-registry/master/cluster-registry-crd.yaml
	kubectl config use-context ${hub_cluster_name}
	set +e
	make -C registration-operator deploy-hub ${ocm_registration_operator_make_arguments} #OLM_VERSION=latest
	# FATA[0000] Failed to run packagemanifests: create catalog: error creating catalog source: catalogsources.operators.coreos.com "cluster-manager-catalog" already exists
	set -e
	date
	kubectl --context ${hub_cluster_name} -n olm                     wait deployments --all --for condition=available --timeout 1m
	date
	kubectl --context ${hub_cluster_name} -n open-cluster-management wait deployments --all --for condition=available
	date
	# https://github.com/kubernetes/kubernetes/issues/83242
	until_true_or_n 90 kubectl --context ${hub_cluster_name} -n open-cluster-management-hub wait deployments --all --for condition=available --timeout 0
}
ocm_registration_operator_undeploy_hub()
{
	kubectl config use-context ${hub_cluster_name}
	set +e
	make -C registration-operator clean-hub ${ocm_registration_operator_make_arguments}
	# error: unable to recognize "deploy/cluster-manager/config/samples/operator_open-cluster-management_clustermanagers.cr.yaml": no matches for kind "ClusterManager" in version "operator.open-cluster-management.io/v1"
	set -e
}
application_sample_0_deploy()
{
	mkdir -p /tmp/$USER/ocm-minikube
	cp -R ocm-minikube/examples /tmp/$USER/ocm-minikube
	sed -e "s,KIND_CLUSTER,${1}," -i /tmp/$USER/ocm-minikube/examples/kustomization.yaml
	kubectl --context ${hub_cluster_name} apply -k /tmp/$USER/ocm-minikube/examples
	condition=ready
	condition=initialized
	# Failed to pull image "busybox": rpc error: code = Unknown desc = Error response from daemon: toomanyrequests: You have reached your pull rate limit. You may increase the limit by authenticating and upgrading: https://www.docker.com/increase-rate-limit
	until_true_or_n 150 kubectl --context ${1} wait pods/hello --for condition=${condition} --timeout 0
	unset -v condition
}
application_sample_0_undeploy()
{
	# delete may exceed 30 seconds
	date
	kubectl --context ${hub_cluster_name} delete -k /tmp/$USER/ocm-minikube/examples #--wait=false
	date
	# sed -e "s,${1},KIND_CLUSTER," -i /tmp/$USER/ocm-minikube/examples/kustomization.yaml
	set +e
	kubectl --context ${1} wait pods/hello --for delete --timeout 0
	# --wait=true  error: no matching resources found
	# --wait=false error: timed out waiting for the condition on pods/hello
	set -e
	date
}
application_sample_0_test()
{
	application_sample_0_deploy ${1}
	application_sample_0_undeploy ${1}
}
spoke_add()
{
	kubectl config use-context ${1}
	set +e
	MANAGED_CLUSTER=${1}\
	HUB_KUBECONFIG=${HUB_KUBECONFIG}\
	make -C registration-operator deploy-spoke ${ocm_registration_operator_make_arguments}
	# FATA[0000] Failed to run packagemanifests: create catalog: error creating catalog source: catalogsources.operators.coreos.com "klusterlet-catalog" already exists
	set -e

	date
	kubectl --context ${1} -n open-cluster-management wait deployments --all --for condition=available
	date
	# https://github.com/kubernetes/kubernetes/issues/83242
	until_true_or_n 90 kubectl --context ${1} -n open-cluster-management-agent wait deployments/klusterlet-registration-agent --for condition=available --timeout 0

	# hub register managed cluster
	until_true_or_n 30 kubectl --context ${hub_cluster_name} get managedclusters/${1}
	kubectl --context ${1} apply -f registration-operator/vendor/github.com/open-cluster-management/api/work/v1/0000_00_work.open-cluster-management.io_manifestworks.crd.yaml
	set +e
	kubectl --context ${hub_cluster_name} certificate approve $(kubectl --context ${hub_cluster_name} get csr --field-selector spec.signerName=kubernetes.io/kube-apiserver-client --selector open-cluster-management.io/cluster-name=${1} -oname)
	# error: one or more CSRs must be specified as <name> or -f <filename>
	kubectl --context ${hub_cluster_name} patch managedclusters/${1} -p '{"spec":{"hubAcceptsClient":true}}' --type=merge
	# Error from server (InternalError): Internal error occurred: failed calling webhook "managedclustermutators.admission.cluster.open-cluster-management.io": the server is currently unable to handle the request
	set -e
	date
	kubectl --context ${hub_cluster_name} wait managedclusters/${1} --for condition=ManagedClusterConditionAvailable
	date
	kubectl --context ${1} -n open-cluster-management-agent wait deployments --all --for condition=available --timeout 60s
	date
	#application_sample_0_test ${1}
}
ocm_registration_operator_undeploy_spoke()
{
	kubectl config use-context ${1}
	set +e
	make -C registration-operator clean-spoke ${ocm_registration_operator_make_arguments}
	# error: unable to recognize "deploy/klusterlet/config/samples/operator_open-cluster-management_klusterlets.cr.yaml": no matches for kind "Klusterlet" in version "operator.open-cluster-management.io/v1"
	set -e
}
ocm_subscription_operator_checkout()
{
	set -- multicloud-operators-subscription
	set +e
	git clone https://github.com/open-cluster-management/${1}
	# fatal: destination path 'multicloud-operators-subscription' already exists and is not an empty directory.
	set -e
	set +e
	git apply --directory ${1} ocm-minikube/${1}.diff
	# error: patch failed: Makefile:209
	# error: Makefile: patch does not apply
	set -e
}
ocm_subscription_operator_deploy_hub()
{
	kubectl config use-context ${hub_cluster_name}
	USE_VENDORIZED_BUILD_HARNESS=faked make -C multicloud-operators-subscription deploy-community-hub
	date
	kubectl --context ${hub_cluster_name} -n multicluster-operators wait deployments --all --for condition=available --timeout 2m
	date
}
ocm_subscription_operator_deploy_spoke_hub()
{
	cp -f ${HUB_KUBECONFIG} /tmp/$USER/kubeconfig
	kubectl --context ${1} -n multicluster-operators delete secret appmgr-hub-kubeconfig --ignore-not-found
	kubectl --context ${1} -n multicluster-operators create secret generic appmgr-hub-kubeconfig --from-file=kubeconfig=/tmp/$USER/kubeconfig
	mkdir -p multicloud-operators-subscription/munge-manifests
	cp multicloud-operators-subscription/deploy/managed/operator.yaml multicloud-operators-subscription/munge-manifests/operator.yaml
	sed -i 's/<managed cluster name>/'"${1}"'/g' multicloud-operators-subscription/munge-manifests/operator.yaml
	sed -i 's/<managed cluster namespace>/'"${1}"'/g' multicloud-operators-subscription/munge-manifests/operator.yaml
	sed -i '0,/name: multicluster-operators-subscription/{s/name: multicluster-operators-subscription/name: multicluster-operators-subscription-mc/}' multicloud-operators-subscription/munge-manifests/operator.yaml
	kubectl --context ${1} apply -f multicloud-operators-subscription/munge-manifests/operator.yaml
	date
	kubectl --context ${1} -n multicluster-operators wait deployments --all --for condition=available
	date
}
ocm_subscription_operator_deploy_spoke_nonhub()
{
	kubectl config use-context ${1}
	MANAGED_CLUSTER_NAME=${1}\
	HUB_KUBECONFIG=${HUB_KUBECONFIG}\
	USE_VENDORIZED_BUILD_HARNESS=faked\
	make -C multicloud-operators-subscription deploy-community-managed
	date
	kubectl --context ${1} -n multicluster-operators wait deployments --all --for condition=available --timeout 1m
	date
}
spoke_test_deploy()
{
	kubectl --context ${hub_cluster_name} apply -f multicloud-operators-subscription/examples/helmrepo-hub-channel
	# https://github.com/kubernetes/kubernetes/issues/83242
	until_true_or_n 60 kubectl --context ${1} wait deployments --selector app=nginx-ingress --for condition=available --timeout 0
}
spoke_test_undeploy()
{
	kubectl --context ${hub_cluster_name} delete -f multicloud-operators-subscription/examples/helmrepo-hub-channel
	set +e
	kubectl --context ${1} wait deployments --selector app=nginx-ingress --for delete --timeout 1m
	# error: no matching resources found
	set -e
}
spoke_test()
{
	spoke_test_deploy ${1}
	spoke_test_undeploy ${1}
}
spoke_add_hub()
{
	spoke_add ${1}
	kubectl --context ${hub_cluster_name} label managedclusters/${1} local-cluster=true --overwrite
	ocm_subscription_operator_deploy_spoke_hub ${1}
}
spoke_add_nonhub()
{
	minikube start ${minikube_start_options} --profile=${1}
	spoke_add ${1}
	ocm_subscription_operator_deploy_spoke_nonhub ${1}
	#spoke_test ${1}
}
ocm_application_samples_checkout()
{
	set -- application-samples
	set +e
	git clone https://github.com/open-cluster-management/${1}
	# fatal: destination path 'application-samples' already exists and is not an empty directory.
	set -e
	set +e
	git apply --directory ${1} ocm-minikube/${1}.diff
	# error: patch failed: subscriptions/book-import/placementrule.yaml:9
	# error: subscriptions/book-import/placementrule.yaml: patch does not apply
	# error: patch failed: subscriptions/book-import/subscription.yaml:3
	# error: subscriptions/book-import/subscription.yaml: patch does not apply
	set -e
}
application_sample_deploy()
{
	kubectl --context ${hub_cluster_name} apply -k application-samples/subscriptions/channel
	kubectl --context ${hub_cluster_name} label managedclusters/${1} usage=test --overwrite
	set +e
	kubectl --context ${hub_cluster_name} apply -k application-samples/subscriptions/book-import
	# Error from server (InternalError): error when creating "subscriptions/book-import": Internal error occurred: failed calling webhook "applications.apps.open-cluster-management.webhook": Post "https://multicluster-operators-application-svc.multicluster-operators.svc:443/app-validate?timeout=10s": dial tcp 10.106.210.84:443: connect: connection refused
	set -e
	# https://github.com/kubernetes/kubernetes/issues/83242
	until_true_or_n 30 kubectl --context ${1} -n book-import wait deployments --all --for condition=available --timeout 0
}
application_sample_undeploy()
{
	kubectl --context ${hub_cluster_name} delete -k application-samples/subscriptions/book-import --ignore-not-found
	# Error from server (NotFound): error when deleting "application-samples/subscriptions/book-import": applications.app.k8s.io "book-import" not found
	date
	set +e
	kubectl --context ${1} -n book-import wait pods --all --for delete --timeout 1m
	# error: no matching resources found
	set -e
	date
	kubectl --context ${1} delete namespaces/book-import
	date
	kubectl --context ${hub_cluster_name} label managedclusters/${1} usage-
	kubectl --context ${hub_cluster_name} delete -k application-samples/subscriptions/channel
}
application_sample_test()
{
	ocm_application_samples_checkout
	application_sample_deploy ${1}
	application_sample_undeploy ${1}
}
hub_cluster_name=${hub_cluster_name:-hub}
spoke_cluster_names=${spoke_cluster_names:-${hub_cluster_name}\ cluster1}
for cluster_name in ${spoke_cluster_names}; do
	if test ${cluster_name} = ${hub_cluster_name}; then
		spoke_cluster_names_hub=${spoke_cluster_names_hub}\ ${cluster_name}
	else
		spoke_cluster_names_nonhub=${spoke_cluster_names_nonhub}\ ${cluster_name}
	fi
done
for command in "${@:-deploy}"; do
        case ${command} in
        deploy)
		minikube start ${minikube_start_options} --profile=${hub_cluster_name} --cpus=4
		ocm_minikube_checkout
		ocm_registration_operator_checkout
		ocm_registration_operator_deploy_hub
		ocm_subscription_operator_checkout
		ocm_subscription_operator_deploy_hub
		mkdir -p /tmp/$USER
		HUB_KUBECONFIG=/tmp/$USER/${hub_cluster_name}-config
		kubectl --context ${hub_cluster_name} config view --flatten --minify >${HUB_KUBECONFIG}
		for cluster_name in ${spoke_cluster_names_hub}   ; do spoke_add_hub    ${cluster_name}; done; unset -v cluster_name
		for cluster_name in ${spoke_cluster_names_nonhub}; do spoke_add_nonhub ${cluster_name}; done; unset -v cluster_name
		unset -v HUB_KUBECONFIG
		;;
	test)
		for cluster_name in ${spoke_cluster_names_nonhub}; do
			application_sample_test ${cluster_name}
			break
		done; unset -v cluster_name
		;;
	ocm_registration_operator_undeploy_spoke_hub)
		for cluster_name in ${spoke_cluster_names_hub}; do
			ocm_registration_operator_undeploy_spoke ${cluster_name}
		done; unset -v cluster_name
		;;
	ocm_registration_operator_undeploy_hub)
		ocm_registration_operator_undeploy_hub
		;;
	undeploy)
		for cluster_name in ${spoke_cluster_names_nonhub} ${spoke_cluster_names_hub}; do
			ocm_registration_operator_undeploy_spoke ${cluster_name}
		done; unset -v cluster_name
		ocm_registration_operator_undeploy_hub
		;;
	delete)
		for cluster_name in ${spoke_cluster_names_nonhub} ${hub_cluster_name}; do
			minikube delete -p ${cluster_name}
		done; unset -v cluster_name
		;;
	esac
done
unset -v spoke_cluster_names_nonhub
unset -v spoke_cluster_names_hub
unset -v spoke_cluster_names
unset -v hub_cluster_name
unset -f application_sample_test
unset -f application_sample_undeploy
unset -f application_sample_deploy
unset -f ocm_application_samples_checkout
unset -f spoke_add_nonhub
unset -f spoke_add_hub
unset -f spoke_add
unset -f spoke_test
unset -f spoke_test_undeploy
unset -f spoke_test_deploy
unset -f application_sample_0_test
unset -f application_sample_0_undeploy
unset -f application_sample_0_deploy
unset -f ocm_subscription_operator_deploy_spoke_nonhub
unset -f ocm_subscription_operator_deploy_spoke_hub
unset -f ocm_subscription_operator_deploy_hub
unset -f ocm_subscription_operator_checkout
unset -f ocm_registration_operator_undeploy_spoke
unset -f ocm_registration_operator_undeploy_hub
unset -f ocm_registration_operator_deploy_hub
unset -v ocm_registration_operator_make_arguments
unset -f ocm_registration_operator_checkout
unset -f ocm_minikube_checkout
unset -f until_true_or_n
unset -v minikube_start_options
