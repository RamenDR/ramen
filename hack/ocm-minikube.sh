#!/bin/sh
# shellcheck disable=SC2046,SC2086

# open cluster management (ocm) hub and managed minikube kvm amd64 clusters deploy
# https://github.com/ShyamsundarR/ocm-minikube/README.md

set -x
set -e
trap 'echo exit value: $?' EXIT

#minikube delete -p cluster2
#minikube delete -p cluster1
#minikube delete -p hub

mkdir -p ${HOME}/.local/bin
PATH=${HOME}/.local/bin:${PATH}

if ! command -v curl; then
	wget -O ${HOME}/.local/bin/curl https://github.com/moparisthebest/static-curl/releases/download/v7.76.0/curl-amd64
	chmod +x ${HOME}/.local/bin/curl
fi

if ! command -v minikube; then
	# https://minikube.sigs.k8s.io/docs/start/
	minikube_version=latest
	minikube_version=v1.18.1
	curl -LRo ${HOME}/.local/bin/minikube https://storage.googleapis.com/minikube/releases/${minikube_version}/minikube-linux-amd64
	unset -v minikube_version
	chmod +x ${HOME}/.local/bin/minikube
fi

minikube_start_options=--driver=kvm2
# shellcheck disable=SC1091
. /etc/os-release # NAME

if false; then
	# https://minikube.sigs.k8s.io/docs/drivers/kvm2/
	case ${NAME} in
	"Red Hat Enterprise Linux Server")
		# https://access.redhat.com/articles/1344173#Q_how-install-virtualization-packages
		sudo yum install libvirt -y
		;;
	"Ubuntu")
		# https://help.ubuntu.com/community/KVM/Installation
		sudo apt-get update
		if true || test ${VERSION_ID} -ge "18.10"; then
			sudo apt-get install qemu-kvm libvirt-daemon-system libvirt-clients bridge-utils -y
		else
			sudo apt-get install qemu-kvm libvirt-bin ubuntu-vm-builder bridge-utils -y
		fi
		;;
	esac
	sudo usermod -aG libvirt ${LOGNAME}
	# groups refresh
	exec su - ${LOGNAME}
fi

minikube start ${minikube_start_options} --profile=hub --cpus=4

# TODO or less than version 1.11 (wait unsupported)
if ! command -v kubectl; then
	# https://kubernetes.io/docs/tasks/tools/install-kubectl-linux/#install-kubectl-binary-with-curl-on-linux
	curl -LRo ${HOME}/.local/bin/kubectl https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl
	chmod +x ${HOME}/.local/bin/kubectl
fi

if ! command -v kustomize; then
	curl -L https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2Fv4.0.5/kustomize_v4.0.5_linux_amd64.tar.gz | tar -C${HOME}/.local/bin -xz
fi

until_true_or_n()
{
	set +x
	n=${1}
	shift
	i=0
	date
	while ! "${@}"
	do
		test ${i} -lt ${n}
		sleep 1
		i=$((i+1))
	done
	date
	unset -v i
	unset -v n
	set -x
}

kubectl apply -f https://raw.githubusercontent.com/kubernetes/cluster-registry/master/cluster-registry-crd.yaml
set +e
git clone https://github.com/open-cluster-management/registration-operator
# fatal: destination path 'registration-operator' already exists and is not an empty directory.
git clone https://github.com/ShyamsundarR/ocm-minikube
# fatal: destination path 'ocm-minikube' already exists and is not an empty directory.
set -e
# registration-operator make deploy-hub requires go version 1.14.4 or greater
	# https://golang.org/ref/mod#versions
	# https://semver.org/spec/v2.0.0.html

case ${NAME} in
"Ubuntu")
	deploy_arguments=GO_REQUIRED_MIN_VERSION:=
	;;
esac
if ! command -v go
# TODO or version less than 1.14.4?
#|| $(go version | { read _ _ v _; echo ${v#go}; })
then
	PATH=${HOME}/.local/go/bin:${PATH}
	if ! command -v go; then
		curl -L https://golang.org/dl/go1.16.2.linux-amd64.tar.gz | tar -C${HOME}/.local -xz
	fi
fi

cd registration-operator
git checkout release-2.3
# managed cluster names other than cluster1 require
set +e
git apply ../ocm-minikube/registration-operator.diff
# error: patch failed: Makefile:36
# error: Makefile: patch does not apply
set -e
set +e
make deploy-hub ${deploy_arguments}
# FATA[0000] Failed to run packagemanifests: create catalog: error creating catalog source: catalogsources.operators.coreos.com "cluster-manager-catalog" already exists
set -e
cd ..
date
kubectl --context hub -n olm                     wait deployments --all --for condition=available --timeout 1m
date
kubectl --context hub -n open-cluster-management wait deployments --all --for condition=available
date
# https://github.com/kubernetes/kubernetes/issues/83242
until_true_or_n 90 kubectl --context hub -n open-cluster-management-hub wait deployments --all --for condition=available --timeout 0

HUB_KUBECONFIG=/tmp/hub-config
kubectl --context hub config view --flatten --minify >${HUB_KUBECONFIG}

spoke_add()
{
	cluster_name=${1}
	cd registration-operator
	kubectl config use-context ${cluster_name}
	set +e
	MANAGED_CLUSTER=${cluster_name}\
	HUB_KUBECONFIG=${HUB_KUBECONFIG}\
	make deploy-spoke ${deploy_arguments}
	# FATA[0000] Failed to run packagemanifests: create catalog: error creating catalog source: catalogsources.operators.coreos.com "klusterlet-catalog" already exists
	set -e
	cd ..

	date
	kubectl --context ${cluster_name} -n open-cluster-management wait deployments --all --for condition=available
	date
	# https://github.com/kubernetes/kubernetes/issues/83242
	until_true_or_n 60 kubectl --context ${cluster_name} -n open-cluster-management-agent wait deployments/klusterlet-registration-agent --for condition=available --timeout 0

	# hub register managed cluster
	until_true_or_n 30 kubectl --context hub get managedclusters/${cluster_name} --ignore-not-found=false
	set +e
	kubectl --context hub certificate approve $(kubectl --context hub get csr --field-selector spec.signerName=kubernetes.io/kube-apiserver-client --selector open-cluster-management.io/cluster-name=${cluster_name} -oname)
	# error: one or more CSRs must be specified as <name> or -f <filename>
	set -e
	kubectl --context hub patch managedclusters/${cluster_name} -p='{"spec":{"hubAcceptsClient":true}}' --type=merge
	date
	kubectl --context hub wait managedclusters/${cluster_name} --for condition=ManagedClusterConditionAvailable
	date
	kubectl --context ${cluster_name} -n open-cluster-management-agent wait deployments --all --for condition=available
	date

	# test
	mkdir -p /tmp/ocm-minikube
	cp -R ocm-minikube/examples /tmp/ocm-minikube
	sed -e "s,KIND_CLUSTER,${cluster_name}," -i /tmp/ocm-minikube/examples/kustomization.yaml
	kubectl --context hub apply -k /tmp/ocm-minikube/examples
	condition=ready
	condition=initialized
	# Failed to pull image "busybox": rpc error: code = Unknown desc = Error response from daemon: toomanyrequests: You have reached your pull rate limit. You may increase the limit by authenticating and upgrading: https://www.docker.com/increase-rate-limit
	until_true_or_n 150 kubectl --context ${cluster_name} wait pods/hello --for condition=${condition} --timeout 0
	unset -v condition
	# delete may exceed 30 seconds
	date
	kubectl --context hub delete -k /tmp/ocm-minikube/examples --wait=false
	date
	# sed -e "s,${cluster_name},KIND_CLUSTER," -i /tmp/ocm-minikube/examples/kustomization.yaml
	set +e
	kubectl --context ${cluster_name} wait pods/hello --for delete --timeout 0
	# --wait=true  error: no matching resources found
	# --wait=false error: timed out waiting for the condition on pods/hello
	set -e
	date
	unset -v cluster_name
}

# hub subscription operator
set +e
git clone https://github.com/open-cluster-management/multicloud-operators-subscription
# fatal: destination path 'multicloud-operators-subscription' already exists and is not an empty directory.
set -e
cd multicloud-operators-subscription
set +e
git apply ../ocm-minikube/multicloud-operators-subscription.diff
# error: patch failed: Makefile:209
# error: Makefile: patch does not apply
set -e
kubectl config use-context hub
USE_VENDORIZED_BUILD_HARNESS=faked make deploy-community-hub
cd ..

date
kubectl --context hub -n multicluster-operators wait deployments --all --for condition=available --timeout 2m
date

spoke_add_hub()
{
	spoke_add ${1}
	kubectl --context hub patch managedclusters/${1} -p='{"metadata":{"labels":{"local-cluster":"true"}}}' --type=merge
	# hub managed cluster subscription operator
	cd multicloud-operators-subscription
	cp -f ${HUB_KUBECONFIG} /tmp/kubeconfig
	kubectl --context ${1} -n multicluster-operators delete secret appmgr-hub-kubeconfig --ignore-not-found
	kubectl --context ${1} -n multicluster-operators create secret generic appmgr-hub-kubeconfig --from-file=kubeconfig=/tmp/kubeconfig
	mkdir -p munge-manifests
	cp deploy/managed/operator.yaml munge-manifests/operator.yaml
	sed -i 's/<managed cluster name>/'"${1}"'/g' munge-manifests/operator.yaml
	sed -i 's/<managed cluster namespace>/'"${1}"'/g' munge-manifests/operator.yaml
	sed -i '0,/name: multicluster-operators-subscription/{s/name: multicluster-operators-subscription/name: multicluster-operators-subscription-mc/}' munge-manifests/operator.yaml
	kubectl --context ${1} apply -f munge-manifests/operator.yaml
	date
	kubectl --context ${1} -n multicluster-operators wait deployments --all --for condition=available
	date
	cd ..
}
spoke_add_nonhub()
{
	minikube start ${minikube_start_options} --profile=${1}
	spoke_add ${1}
	# managed cluster subscription operator
	cd multicloud-operators-subscription
	kubectl config use-context ${1}
	MANAGED_CLUSTER_NAME=${1}\
	HUB_KUBECONFIG=${HUB_KUBECONFIG}\
	USE_VENDORIZED_BUILD_HARNESS=faked\
	make deploy-community-managed
	date
	kubectl --context ${1} -n multicluster-operators wait deployments --all --for condition=available
	date
	# test
	kubectl --context hub apply -f examples/helmrepo-hub-channel
	# https://github.com/kubernetes/kubernetes/issues/83242
	until_true_or_n 60 kubectl --context ${1} wait deployments --selector app=nginx-ingress --for condition=available --timeout 0
	kubectl --context hub delete -f examples/helmrepo-hub-channel
	set +e
	kubectl --context ${1} wait deployments --selector app=nginx-ingress --for delete --timeout 1m
	# error: no matching resources found
	set -e
	cd ..
}
spoke_add_nonhub cluster1
spoke_add_hub hub
#spoke_add_nonhub cluster2
unset -f spoke_add_nonhub
unset -f spoke_add_hub
unset -f spoke_add
unset -v HUB_KUBECONFIG
unset -v minikube_start_options
unset -v deploy_arguments

# application samples test
cluster_name=cluster1
kubectl --context hub patch managedclusters/${cluster_name} -p='{"metadata":{"labels":{"usage":"test"}}}' --type=merge
set +e
git clone https://github.com/open-cluster-management/application-samples
# fatal: destination path 'application-samples' already exists and is not an empty directory.
set -e
cd application-samples
set +e
git apply ../ocm-minikube/application-samples.diff
# error: patch failed: subscriptions/book-import/placementrule.yaml:9
# error: subscriptions/book-import/placementrule.yaml: patch does not apply
# error: patch failed: subscriptions/book-import/subscription.yaml:3
# error: subscriptions/book-import/subscription.yaml: patch does not apply
set -e
kubectl --context hub apply -k subscriptions/channel
set +e
kubectl --context hub apply -k subscriptions/book-import
# Error from server (InternalError): error when creating "subscriptions/book-import": Internal error occurred: failed calling webhook "applications.apps.open-cluster-management.webhook": Post "https://multicluster-operators-application-svc.multicluster-operators.svc:443/app-validate?timeout=10s": dial tcp 10.106.210.84:443: connect: connection refused
kubectl --context hub apply -f subscriptions/book-import/application.yaml
# Error from server (InternalError): error when creating "subscriptions/book-import/application.yaml": Internal error occurred: failed calling webhook "applications.apps.open-cluster-management.webhook": Post "https://multicluster-operators-application-svc.multicluster-operators.svc:443/app-validate?timeout=10s": dial tcp 10.106.210.84:443: connect: connection refused
set -e
# https://github.com/kubernetes/kubernetes/issues/83242
until_true_or_n 30 kubectl --context ${cluster_name} -n book-import wait deployments --all --for condition=available --timeout 0
set +e
kubectl --context hub delete -f subscriptions/book-import/application.yaml
# Error from server (NotFound): error when deleting "subscriptions/book-import/application.yaml": applications.app.k8s.io "book-import" not found
kubectl --context hub delete -k subscriptions/book-import
# Error from server (NotFound): error when deleting "subscriptions/book-import": applications.app.k8s.io "book-import" not found
set -e
kubectl --context hub delete -k subscriptions/channel
date
set +e
kubectl --context ${cluster_name} -n book-import wait pods --all --for delete --timeout 1m
# error: no matching resources found
set -e
date
cd ../
unset -v cluster_name
unset -f until_true_or_n
