#!/bin/sh
# shellcheck disable=1091,2046,2086

# open cluster management (ocm) hub and managed minikube kvm amd64 clusters deploy
# https://github.com/ShyamsundarR/ocm-minikube/README.md

set -x
set -e
ramen_hack_directory_path_name=$(dirname $0)
# shellcheck source=./exit_stack.sh
. $ramen_hack_directory_path_name/exit_stack.sh

mkdir -p ${HOME}/.local/bin
PATH=${HOME}/.local/bin:${PATH}

${ramen_hack_directory_path_name}/minikube-install.sh ${HOME}/.local/bin
# 1.11 wait support
# 1.19 certificates.k8s.io/v1 https://github.com/kubernetes/kubernetes/pull/91685
# 1.21 kustomize v4.0.5 https://github.com/kubernetes-sigs/kustomize#kubectl-integration
${ramen_hack_directory_path_name}/kubectl-install.sh ${HOME}/.local/bin 1 21
${ramen_hack_directory_path_name}/kustomize-install.sh ${HOME}/.local/bin
# shellcheck source=./until_true_or_n.sh
. ${ramen_hack_directory_path_name}/until_true_or_n.sh
# TODO registration-operator go version minimum determine programatically
# shellcheck source=./go-install.sh
. ${ramen_hack_directory_path_name}/go-install.sh; go_install ${HOME}/.local 1.15.2; unset -f go_install
# shellcheck source=./git-checkout.sh
. $ramen_hack_directory_path_name/git-checkout.sh
exit_stack_push git_checkout_unset
# shellcheck source=./github-url.sh
. $ramen_hack_directory_path_name/github-url.sh
exit_stack_push github_url_unset
unset -v ramen_hack_directory_path_name

minikube_start_options="--driver=kvm2 --network=default"
minikube_start_additional_hub_options=--cpus=4
. /etc/os-release # NAME

ensure_libvirt_default_network_exists()
{
	if ! virsh net-dumpxml default >/dev/null 2>&1 ; then
		echo 'libvirt network default is required as it is used as the common network for all the minikube instances'
		exit 1
	fi
}

minikube_validate()
{
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
	ensure_libvirt_default_network_exists
}

json6902_test_and_replace_yaml()
{
	printf '
      - op: test
        path: %s
        value: %s
      - op: replace
        path: %s
        value: %s' $1 "$2" $1 "$3"
}
exit_stack_push unset -f json6902_test_and_replace_yaml
ocm_registration_operator_git_ref=c723e190c454110797d89e67bcb33250b35e1fd7
exit_stack_push unset -v ocm_registration_operator_git_ref
ocm_registration_operator_image_spec()
{
	set -- /spec/$1ImagePullSpec quay.io/open-cluster-management/$1
	json6902_test_and_replace_yaml $1 $2 $2:latest
}
exit_stack_push unset -f ocm_registration_operator_image_spec
ocm_registration_operator_image_specs()
{
	for image_name in "$@"; do
		ocm_registration_operator_image_spec $image_name
	done; unset -v image_name
}
exit_stack_push unset -f ocm_registration_operator_image_specs
ocm_registration_operator_kubectl()
{
	kubectl --context $1 $3 -k https://github.com/stolostron/registration-operator/deploy/$2/config?ref=$ocm_registration_operator_git_ref
}
exit_stack_push unset -f ocm_registration_operator_kubectl
ocm_registration_operator_kustomization_directory_path_name()
{
	echo /tmp/$USER/open-cluster-management/registration/$1/$2
}
exit_stack_push unset -f ocm_registration_operator_kustomization_directory_path_name
ocm_registration_operator_cr_kubectl()
{
	set -- $1 $2 $3 $4 "$5" "$6" "$7" $(ocm_registration_operator_kustomization_directory_path_name $3 $1)
	mkdir -p $8
	cat <<-a >$8/kustomization.yaml
	resources:
	  - https://raw.githubusercontent.com/stolostron/registration-operator/$ocm_registration_operator_git_ref/deploy/$3/config/samples/operator_open-cluster-management_$4s.cr.yaml
	patchesJson6902:
	  - target:
	      group: operator.open-cluster-management.io
	      version: v1
	      kind: $2
	      name: $3
	    patch: |-$(ocm_registration_operator_image_specs registration work $5)$6
	a
	kubectl --context $1 $7 -k $8
}
exit_stack_push unset -f ocm_registration_operator_cr_kubectl
ocm_registration_operator_deploy()
{
	set -- $1 $2 $3 $4 "$5" "$6" apply
	ocm_registration_operator_kubectl $1 $3 $7
	ocm_registration_operator_cr_kubectl $1	$2 $3 $4 "$5" "$6" $7
}
exit_stack_push unset -f ocm_registration_operator_deploy
ocm_registration_operator_undeploy()
{
	set -- $1 $2 $3 $4 "$5" "$6" delete\ --ignore-not-found
	ocm_registration_operator_cr_kubectl $1 $2 $3 $4 "$5" "$6" "$7"
	ocm_registration_operator_kubectl $1 $3 "$7"
}
exit_stack_push unset -f ocm_registration_operator_undeploy
ocm_registration_operator_hub()
{
	ocm_registration_operator_$2 $1 ClusterManager cluster-manager clustermanager placement ''
}
exit_stack_push unset -f ocm_registration_operator_hub
ocm_registration_operator_spoke()
{
	ocm_registration_operator_$2 $1 Klusterlet klusterlet klusterlet '' \
		"$(json6902_test_and_replace_yaml /spec/clusterName cluster1 $1)"
}
exit_stack_push unset -f ocm_registration_operator_spoke
ocm_registration_operator_deploy_hub()
{
	set -- $hub_cluster_name
	#kubectl --context $1 apply -f https://raw.githubusercontent.com/kubernetes/cluster-registry/master/cluster-registry-crd.yaml
	ocm_registration_operator_hub $1 deploy
	date
	kubectl --context $1 -n open-cluster-management wait deployments/cluster-manager --for condition=available
	date
	# https://github.com/kubernetes/kubernetes/issues/83242
	until_true_or_n 90 kubectl --context $1 -n open-cluster-management-hub wait deployments --all --for condition=available --timeout 0
}
exit_stack_push unset -f ocm_registration_operator_deploy_hub
ocm_registration_operator_undeploy_hub()
{
	set -- $hub_cluster_name
	ocm_registration_operator_hub $1 undeploy
	#kubectl --context $1 delete -f https://raw.githubusercontent.com/kubernetes/cluster-registry/master/cluster-registry-crd.yaml
}
exit_stack_push unset -f ocm_registration_operator_undeploy_hub
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
ocm_registration_operator_bootstrap_kubectl()
{
	set -- $1 "$2" $(ocm_registration_operator_kustomization_directory_path_name klusterlet $1)/bootstrap
	mkdir -p $3
	cat <<-a >$3/kustomization.yaml
	secretGenerator:
	  - name: bootstrap-hub-kubeconfig
	    namespace: open-cluster-management-agent
	    files:
	      - kubeconfig=hub-kubeconfig
	    type: Opaque
	generatorOptions:
	  disableNameSuffixHash: true
	a
	cp -f $hub_kubeconfig_file_path_name $3/hub-kubeconfig
	kubectl --context $1 $2 -k $3
}
exit_stack_push unset -f ocm_registration_operator_bootstrap_kubectl
ocm_registration_operator_deploy_spoke()
{
	kubectl create namespace open-cluster-management-agent --dry-run=client -o yaml | kubectl --context $1 apply -f -
	ocm_registration_operator_bootstrap_kubectl $1 apply
	ocm_registration_operator_spoke $1 deploy
	date
	kubectl --context $1 -n open-cluster-management wait deployments/klusterlet --for condition=available --timeout 60s
	date
	# https://github.com/kubernetes/kubernetes/issues/83242
	until_true_or_n 90 kubectl --context ${1} -n open-cluster-management-agent wait deployments/klusterlet-registration-agent --for condition=available --timeout 0

	# hub register managed cluster
	until_true_or_n 30 kubectl --context ${hub_cluster_name} get managedclusters/${1}
	set +e
	kubectl --context ${hub_cluster_name} certificate approve $(kubectl --context ${hub_cluster_name} get csr --field-selector spec.signerName=kubernetes.io/kube-apiserver-client --selector open-cluster-management.io/cluster-name=${1} -oname)
	# error: one or more CSRs must be specified as <name> or -f <filename>
	kubectl --context ${hub_cluster_name} patch managedclusters/${1} -p '{"spec":{"hubAcceptsClient":true}}' --type=merge
	# Error from server (InternalError): Internal error occurred: failed calling webhook "managedclustermutators.admission.cluster.open-cluster-management.io": the server is currently unable to handle the request
	set -e
	date
	kubectl --context ${hub_cluster_name} wait managedclusters/${1} --for condition=ManagedClusterConditionAvailable
	date
	kubectl --context $1 -n open-cluster-management-agent wait deployments/klusterlet-work-agent --for condition=available --timeout 90s
	date
	#application_sample_0_test ${1}
}
exit_stack_push unset -f ocm_registration_operator_deploy_spoke
ocm_registration_operator_undeploy_spoke()
{
	ocm_registration_operator_spoke $1 undeploy
	ocm_registration_operator_bootstrap_kubectl $1 delete\ --ignore-not-found
	kubectl --context $1 delete namespace open-cluster-management-agent --ignore-not-found
}
exit_stack_push unset -f ocm_registration_operator_undeploy_spoke
ocm_foundation_operator_git_ref=${ocm_foundation_operator_git_ref:-dc43ec703e62594e3942c7f06d38d1897550ffea}
exit_stack_push unset -v ocm_foundation_operator_git_ref
ocm_foundation_operator_image_tag=${ocm_foundation_operator_image_name:-2.4.0\-$ocm_foundation_operator_git_ref}
exit_stack_push unset -v ocm_foundation_operator_image_tag
ocm_foundation_operator_kubectl()
{
	set -- foundation/$1 $2 $3 $4
	set -- $1 /tmp/$USER/open-cluster-management/$1/$2 $3 $4
	mkdir -p $2
	cat <<-a >$2/kustomization.yaml
	resources:
	  - https://github.com/stolostron/multicloud-operators-foundation/deploy/$1?ref=$ocm_foundation_operator_git_ref
	namespace: $4
	images:
	  - name: quay.io/open-cluster-management/multicloud-manager
	    newTag: $ocm_foundation_operator_image_tag
	a
	kubectl --context $hub_cluster_name $3 -k $2
}
exit_stack_push unset -f ocm_foundation_operator_kubectl
ocm_foundation_operator_kubectl_hub()
{
	ocm_foundation_operator_kubectl hub $hub_cluster_name $1
}
exit_stack_push unset -f ocm_foundation_operator_kubectl_hub
ocm_foundation_operator_kubectl_spoke()
{
	ocm_foundation_operator_kubectl klusterlet $1 $2 $1
}
exit_stack_push unset -f ocm_foundation_operator_kubectl_spoke
ocm_foundation_operator_deploy_hub()
{
	ocm_foundation_operator_kubectl_hub apply
	kubectl --context $hub_cluster_name delete apiservices v1.clusterview.open-cluster-management.io v1alpha1.clusterview.open-cluster-management.io v1beta1.proxy.open-cluster-management.io
	kubectl --context $hub_cluster_name -n open-cluster-management delete deployments/ocm-proxyserver services/ocm-proxyserver
	# Start of ocm-controller failure workaround for the issue where the
	# controller crashes due to improper rolers and missing CRD issues.
	kubectl --context $hub_cluster_name -n open-cluster-management scale deployments/ocm-controller --replicas=0
	sleep 1
	kubectl --context $hub_cluster_name -n open-cluster-management scale deployments/ocm-controller --replicas=1
	# End of ocm-controller failure workaround for the issue where the
	# controller crashes due to improper rolers and missing CRD issues.
	kubectl --context $hub_cluster_name -n open-cluster-management wait deployments/ocm-controller --for condition=available
}
exit_stack_push unset -f ocm_foundation_operator_deploy_hub
ocm_foundation_operator_undeploy_hub()
{
	set +e
	ocm_foundation_operator_kubectl_hub delete
	# Error from server (NotFound): error when deleting "/tmp/$USER/open-cluster-management/foundation/hub/hub": services "ocm-proxyserver" not found
	# Error from server (NotFound): error when deleting "/tmp/$USER/open-cluster-management/foundation/hub/hub": deployments.apps "ocm-proxyserver" not found
	# Error from server (NotFound): error when deleting "/tmp/$USER/open-cluster-management/foundation/hub/hub": apiservices.apiregistration.k8s.io "v1.clusterview.open-cluster-management.io" not found
	# Error from server (NotFound): error when deleting "/tmp/$USER/open-cluster-management/foundation/hub/hub": apiservices.apiregistration.k8s.io "v1alpha1.clusterview.open-cluster-management.io" not found
	# Error from server (NotFound): error when deleting "/tmp/$USER/open-cluster-management/foundation/hub/hub": apiservices.apiregistration.k8s.io "v1beta1.proxy.open-cluster-management.io" not found
	set -e
	set +e
	kubectl --context $hub_cluster_name -n open-cluster-management wait deployments/ocm-controller --for delete
	# error: no matching resources found
	set -e
}
exit_stack_push unset -f ocm_foundation_operator_undeploy_hub
ocm_foundation_operator_deploy_spoke()
{
	ocm_foundation_operator_kubectl_spoke $1 apply
	until_true_or_n 300 kubectl --context $1 -n open-cluster-management-agent wait deployments/klusterlet-addon-workmgr --for condition=available --timeout 0
}
exit_stack_push unset -f ocm_foundation_operator_deploy_spoke
ocm_foundation_operator_undeploy_spoke()
{
	ocm_foundation_operator_kubectl_spoke $1 delete
	set +e
	kubectl --context $1 -n open-cluster-management-agent wait deployments/klusterlet-addon-workmgr --for delete
	# error: no matching resources found
	set -e
}
exit_stack_push unset -f ocm_foundation_operator_undeploy_spoke
foundation_operator_deploy_spokes()
{
	for_each "$spoke_cluster_names" ocm_foundation_operator_deploy_spoke
}
exit_stack_push unset -f foundation_operator_deploy_spokes
foundation_operator_undeploy_spokes()
{
	for_each "$spoke_cluster_names" ocm_foundation_operator_undeploy_spoke
}
exit_stack_push unset -f foundation_operator_undeploy_spokes
foundation_operator_deploy()
{
	ocm_foundation_operator_deploy_hub
	foundation_operator_deploy_spokes
}
exit_stack_push unset -f foundation_operator_deploy
foundation_operator_undeploy()
{
	foundation_operator_undeploy_spokes
	ocm_foundation_operator_undeploy_hub
}
exit_stack_push unset -f foundation_operator_undeploy
subscription_operator_release_name=2.3
exit_stack_push unset -v subscription_operator_release_name
subscription_operator_git_ref=c48c55969bc4385bc694acd8bc92e5bf4e0181d3
exit_stack_push unset -v subscription_operator_git_ref
subscription_operator_file_names_deploy_common="
apps.open-cluster-management.io_channels_crd.yaml
apps.open-cluster-management.io_deployables_crd.yaml
apps.open-cluster-management.io_helmreleases_crd.yaml
apps.open-cluster-management.io_placementrules_crd.yaml
apps.open-cluster-management.io_subscriptions.yaml
clusterrole.yaml
clusterrole_binding.yaml
namespace.yaml
service.yaml
service_account.yaml
"
subscription_operator_file_names_deploy_hub="
application-operator.yaml
operator.yaml
"
subscription_operator_file_names_deploy_managed="
operator.yaml
"
subscription_operator_file_names_examples_helmrepo_hub_channel="
00-namespace.yaml
01-channel.yaml
02-placement.yaml
02-subscription.yaml
"
exit_stack_push unset -v subscription_operator_file_names_deploy_common
exit_stack_push unset -v subscription_operator_file_names_deploy_hub
exit_stack_push unset -v subscription_operator_file_names_deploy_managed
exit_stack_push unset -v subscription_operator_file_names_examples_helmrepo_hub_channel
subscription_operator_file_url()
{
	github_url_file stolostron/multicloud-operators-subscription $1 $subscription_operator_git_ref
}
exit_stack_push unset -f subscription_operator_file_url
subscription_operator_file_urls()
{
	for file_name in $1; do
		echo "$3"$(subscription_operator_file_url $2$file_name)
	done; unset -v file_name
}
exit_stack_push unset -f subscription_operator_file_urls
subscription_operator_file_urls_kubectl()
{
	kubectl --context $1 $2 $(subscription_operator_file_urls "$3" $4 '-f ')
}
exit_stack_push unset -f subscription_operator_file_urls_kubectl
subscription_operator_file_urls_kustomization()
{
	subscription_operator_file_urls "$1" $2 '  - '
}
exit_stack_push unset -f subscription_operator_file_urls_kustomization
subscription_operator_kubectl_common()
{
	subscription_operator_file_urls_kubectl $1 $2 "$subscription_operator_file_names_deploy_common" deploy/common/
}
exit_stack_push unset -f subscription_operator_kubectl_common
subscription_operator_deploy_common()
{
	subscription_operator_kubectl_common $1 apply
}
exit_stack_push unset -f subscription_operator_deploy_common
subscription_operator_undeploy_common()
{
	subscription_operator_kubectl_common $1 delete
}
exit_stack_push unset -f subscription_operator_undeploy_common
subscription_operator_kubectl()
{
	set -- $1 "$2" $3 "$4" "$5" "$6" open-cluster-management multicluster-operators subscription
	set -- $1 "$2" $3 "$4" "$5" "$6" $7 $8 $9 /tmp/$USER/$7/$9/$1/$2
	mkdir -p ${10}
	cat <<-a >${10}/kustomization.yaml
	resources:
	$(subscription_operator_file_urls_kustomization "$6" deploy/$1/)
	patchesJson6902:
	  - target:
	      group: apps
	      version: v1
	      kind: Deployment
	      name: $8-$9
	      namespace: $8
	    patch: |-\
	$(json6902_test_and_replace_yaml /metadata/name $8-$9 $8-$9$4)\
	$(json6902_test_and_replace_yaml /spec/template/spec/containers/0/image\
		quay.io/$7/$8-$9:latest\
		quay.io/$7/$8-$9:$subscription_operator_release_name-$subscription_operator_git_ref\
	)\
	$5
	a
	kubectl --context $2 $3 -k ${10}
}
exit_stack_push unset -f subscription_operator_kubectl
subscription_operator_kubectl_hub()
{
	subscription_operator_kubectl hub $hub_cluster_name $1 '' '' "$subscription_operator_file_names_deploy_hub"
}
exit_stack_push unset -f subscription_operator_kubectl_hub
subscription_operator_kubectl_spoke()
{
	subscription_operator_kubectl managed $1 $2 "$3" "$4" "$subscription_operator_file_names_deploy_managed"
}
exit_stack_push unset -f subscription_operator_kubectl_spoke
subscription_operator_deploy_hub()
{
	subscription_operator_kubectl_hub apply
	date
	kubectl --context ${hub_cluster_name} -n multicluster-operators wait deployments --all --for condition=available --timeout 2m
	date
}
exit_stack_push unset -f subscription_operator_deploy_hub
subscription_operator_undeploy_hub()
{
	subscription_operator_kubectl_hub delete
}
exit_stack_push unset -f subscription_operator_undeploy_hub
subscription_operator_deploy_spoke()
{
	# https://github.com/open-cluster-management-io/multicloud-operators-subscription/issues/16
	kubectl --context $hub_cluster_name label managedclusters/$1 name=$1 --overwrite
	cp -f $hub_kubeconfig_file_path_name /tmp/$USER/kubeconfig
	kubectl create secret generic appmgr-hub-kubeconfig --from-file=kubeconfig=/tmp/$USER/kubeconfig --dry-run=client -oyaml|kubectl --context $1 -n multicluster-operators apply -f -
	subscription_operator_kubectl_spoke $1 apply "$2" "$(\
	json6902_test_and_replace_yaml /spec/template/spec/containers/0/command/2 --cluster-name='<managed cluster name>' --cluster-name=$1)$(\
	json6902_test_and_replace_yaml /spec/template/spec/containers/0/command/3 --cluster-namespace='<managed cluster namespace>' --cluster-namespace=$1)"
	date
	kubectl --context $1 -n multicluster-operators wait deployments --all --for condition=available --timeout 1m
	date
}
exit_stack_push unset -f subscription_operator_deploy_spoke
subscription_operator_deploy_spoke_hub()
{
	subscription_operator_deploy_spoke $1 -mc
}
exit_stack_push unset -f subscription_operator_deploy_spoke_hub
subscription_operator_deploy_spoke_nonhub()
{
	subscription_operator_deploy_spoke $1 ''
}
exit_stack_push unset -f subscription_operator_deploy_spoke_nonhub
subscription_operator_undeploy_spoke()
{
	subscription_operator_kubectl_spoke $1 delete $2
	kubectl --context $1 -n multicluster-operators delete secret appmgr-hub-kubeconfig
	kubectl --context $hub_cluster_name label managedclusters/$1 name-
}
exit_stack_push unset -f subscription_operator_undeploy_spoke
subscription_operator_undeploy_spoke_hub()
{
	subscription_operator_undeploy_spoke $1 -mc
}
exit_stack_push unset -f subscription_operator_undeploy_spoke_hub
subscription_operator_undeploy_spoke_nonhub()
{
	subscription_operator_undeploy_spoke $1 ''
}
exit_stack_push unset -f subscription_operator_undeploy_spoke_nonhub
subscription_operator_test_kubectl()
{
	set -- $1 $2 /tmp/$USER/open-cluster-management/subscription-test
	mkdir -p $3
	cat <<-a >$3/kustomization.yaml
	resources:
	$(subscription_operator_file_urls_kustomization "$subscription_operator_file_names_examples_helmrepo_hub_channel" examples/helmrepo-hub-channel/)
	patchesJson6902:
	  - target:
	      group: apps.open-cluster-management.io
	      version: v1
	      kind: PlacementRule
	      name: nginx-pr
	    patch: |-
	      - op: add
	        path: /spec/clusterSelector
	        value: {}
	      - op: add
	        path: /spec/clusterSelector/matchLabels
	        value:
	          name: $1
	a
	kubectl --context $hub_cluster_name $2 -k $3
}
exit_stack_push unset -f subscription_operator_test_kubectl
subscription_operator_test_deploy()
{
	subscription_operator_test_kubectl $1 apply
	# https://github.com/kubernetes/kubernetes/issues/83242
	until_true_or_n 90 kubectl --context $1 wait deployments --selector app=nginx-ingress --for condition=available --timeout 0
}
exit_stack_push unset -f subscription_operator_test_deploy
subscription_operator_test_undeploy()
{
	subscription_operator_test_kubectl $1 delete
	set +e
	kubectl --context ${1} wait deployments --selector app=nginx-ingress --for delete --timeout 1m
	# error: no matching resources found
	set -e
}
exit_stack_push unset -f subscription_operator_test_undeploy
subscription_operator_test()
{
	set -- $spoke_cluster_names
	subscription_operator_test_deploy $1
	subscription_operator_test_undeploy $1
}
exit_stack_push unset -f subscription_operator_test
subscription_operator_deploy()
{
	for_each "$hub_cluster_name $spoke_cluster_names_nonhub" subscription_operator_deploy_common
	subscription_operator_deploy_hub
	for_each "$spoke_cluster_names_hub"    subscription_operator_deploy_spoke_hub
	for_each "$spoke_cluster_names_nonhub" subscription_operator_deploy_spoke_nonhub
}
exit_stack_push unset -f subscription_operator_deploy
subscription_operator_undeploy()
{
	for_each "$spoke_cluster_names_nonhub" subscription_operator_undeploy_spoke_nonhub
	for_each "$spoke_cluster_names_hub"    subscription_operator_undeploy_spoke_hub
	subscription_operator_undeploy_hub
	for_each "$spoke_cluster_names_nonhub $hub_cluster_name" subscription_operator_undeploy_common
}
exit_stack_push unset -f subscription_operator_undeploy
policy_operator_deploy_hub()
{
	# Create the namespace
	policy_hub_ns="open-cluster-management"
	kubectl --context ${1} get ns ${policy_hub_ns}
	if [ $(kubectl --context ${1} get ns ${policy_hub_ns} | grep -c ${policy_hub_ns}) -ne 1 ]; then
		kubectl --context ${1} create ns ${policy_hub_ns};
	fi

	# Apply the CRDs
	policy_git_path="https://raw.githubusercontent.com/open-cluster-management-io/governance-policy-propagator/main/deploy"
	kubectl --context ${1} apply -f ${policy_git_path}/crds/policy.open-cluster-management.io_policies.yaml
	kubectl --context ${1} apply -f ${policy_git_path}/crds/policy.open-cluster-management.io_placementbindings.yaml
	kubectl --context ${1} apply -f ${policy_git_path}/crds/policy.open-cluster-management.io_policyautomations.yaml
	kubectl --context ${1} apply -f ${policy_git_path}/crds/policy.open-cluster-management.io_policysets.yaml

	# Deploy the policy-propagator
	kubectl --context ${1} apply -f ${policy_git_path}/operator.yaml -n ${policy_hub_ns}

	# Ensure operator is running
	until_true_or_n 300 kubectl --context ${1} -n ${policy_hub_ns} wait deployments/governance-policy-propagator --for condition=available --timeout 0
}
exit_stack_push unset -f policy_operator_deploy_hub
policy_operator_undeploy_hub()
{
	echo "TODO: Undeploy policy framework on hub!!!"
}
exit_stack_push unset -f policy_operator_undeploy_hub
policy_operator_deploy_spoke()
{
	# Create the namespace
	policy_mc_ns="open-cluster-management-agent-addon"
	if [ $(kubectl --context ${1} get ns ${policy_mc_ns} | grep -c ${policy_mc_ns}) -ne 1 ]; then
		kubectl --context ${1} create ns ${policy_mc_ns};
	fi

	# Create the secret to authenticate with the hub
	kubectl --context ${1} -n ${policy_mc_ns} create secret generic hub-kubeconfig --from-file=kubeconfig=${hub_kubeconfig_file_path_name} --dry-run=client -oyaml|kubectl --context ${1} -n ${policy_mc_ns} apply -f -

	# Apply the policy CRD
	ocm_git_path="https://raw.githubusercontent.com/open-cluster-management-io"
	kubectl --context ${1} apply -f ${ocm_git_path}/governance-policy-propagator/main/deploy/crds/policy.open-cluster-management.io_policies.yaml

	# TODO: Should loop through components and invoke a common routine here
	# Deploy the spec synchronization component
	policy_component="governance-policy-spec-sync"
	kubectl --context ${1} apply -f ${ocm_git_path}/${policy_component}/main/deploy/operator.yaml -n ${policy_mc_ns}
	kubectl --context ${1} set env deployment/${policy_component} -n ${policy_mc_ns} --containers="${policy_component}" WATCH_NAMESPACE=${1}
	# Ensure operator is running
	until_true_or_n 300 kubectl --context ${1} -n ${policy_mc_ns} wait deployments/${policy_component} --for condition=available --timeout 0

	# Deploy the status synchronization component
	policy_component="governance-policy-status-sync"
	kubectl --context ${1} apply -f ${ocm_git_path}/${policy_component}/main/deploy/operator.yaml -n ${policy_mc_ns}
	kubectl --context ${1} set env deployment/${policy_component} -n ${policy_mc_ns} --containers="${policy_component}" WATCH_NAMESPACE=${1}
	# Ensure operator is running
	until_true_or_n 300 kubectl --context ${1} -n ${policy_mc_ns} wait deployments/${policy_component} --for condition=available --timeout 0

	# Deploy the template synchronization component
	policy_component="governance-policy-template-sync"
	kubectl --context ${1} apply -f ${ocm_git_path}/${policy_component}/main/deploy/operator.yaml -n ${policy_mc_ns}
	kubectl --context ${1} set env deployment/${policy_component} -n ${policy_mc_ns} --containers="${policy_component}" WATCH_NAMESPACE=${1}
	# Ensure operator is running
	until_true_or_n 300 kubectl --context ${1} -n ${policy_mc_ns} wait deployments/${policy_component} --for condition=available --timeout 0

	# Apply the configuration policy CRD
	policy_component="config-policy-controller"
	kubectl --context ${1} apply -f ${ocm_git_path}/${policy_component}/main/deploy/crds/policy.open-cluster-management.io_configurationpolicies.yaml

	# Deploy the configuration controller
	kubectl --context ${1} apply -f ${ocm_git_path}/${policy_component}/main/deploy/operator.yaml -n ${policy_mc_ns}
	kubectl --context ${1} set env deployment/${policy_component} -n ${policy_mc_ns} --containers=${policy_component} WATCH_NAMESPACE=${1}
	# Ensure operator is running
	until_true_or_n 300 kubectl --context ${1} -n ${policy_mc_ns} wait deployments/${policy_component} --for condition=available --timeout 0
}
exit_stack_push unset -f policy_operator_deploy_spoke
policy_operator_undeploy_spoke()
{
	echo "TODO: Undeploy policy framework on spoke!!!"
}
exit_stack_push unset -f policy_operator_undeploy_spoke
policy_operator_deploy()
{
	for_each "$hub_cluster_name"    policy_operator_deploy_hub
	for_each "$spoke_cluster_names" policy_operator_deploy_spoke
}
exit_stack_push unset -f policy_operator_deploy
minikube_start()
{
	minikube_validate
	minikube start ${minikube_start_options} --profile=${1} ${2}
}
minikube_start_hub()
{
	minikube_start "${hub_cluster_name}" "${minikube_start_additional_hub_options}"
}
minikube_start_spokes()
{
	for cluster_name in $spoke_cluster_names_nonhub; do minikube_start "${cluster_name}" ; done; unset -v cluster_name
}
spoke_add_hub()
{
	ocm_registration_operator_deploy_spoke $1
	kubectl --context ${hub_cluster_name} label managedclusters/${1} local-cluster=true --overwrite
}
spoke_add_nonhub()
{
	ocm_registration_operator_deploy_spoke $1
}
ocm_application_samples_patch_old_undo()
{
	git_checkout $1 --\ subscriptions/book-import/placementrule.yaml\ subscriptions/book-import/subscription.yaml
}
exit_stack_push unset -f ocm_application_samples_patch_old_undo
ocm_application_samples_patch_apply()
{
	cat <<'a' | git apply --directory $1 $2 -
diff --git a/subscriptions/book-import/kustomization.yaml b/subscriptions/book-import/kustomization.yaml
index 8d35d3a..11d0760 100644
--- a/subscriptions/book-import/kustomization.yaml
+++ b/subscriptions/book-import/kustomization.yaml
@@ -1,5 +1,4 @@
 resources:
 - namespace.yaml
-- application.yaml
 - placementrule.yaml
-- subscription.yaml
\ No newline at end of file
+- subscription.yaml
diff --git a/subscriptions/book-import/placementrule.yaml b/subscriptions/book-import/placementrule.yaml
index ec72faf..293aae9 100644
--- a/subscriptions/book-import/placementrule.yaml
+++ b/subscriptions/book-import/placementrule.yaml
@@ -9,7 +9,7 @@ metadata:
 spec:
   clusterSelector:
     matchLabels:
-      'usage': 'development'
+      'usage': 'test'
   clusterConditions:
     - type: ManagedClusterConditionAvailable
       status: "True"
\ No newline at end of file
diff --git a/subscriptions/book-import/subscription.yaml b/subscriptions/book-import/subscription.yaml
index 69fcb6f..affcc9c 100644
--- a/subscriptions/book-import/subscription.yaml
+++ b/subscriptions/book-import/subscription.yaml
@@ -3,8 +3,8 @@ apiVersion: apps.open-cluster-management.io/v1
 kind: Subscription
 metadata:
   annotations:
-    apps.open-cluster-management.io/git-branch: master
-    apps.open-cluster-management.io/git-path: book-import/app
+    apps.open-cluster-management.io/github-branch: main
+    apps.open-cluster-management.io/github-path: book-import
   labels:
     app: book-import
   name: book-import
a
}
exit_stack_push unset -f ocm_application_samples_patch_apply
ocm_application_samples_checkout()
{
	set -- application-samples
	git_clone_and_checkout https://github.com/stolostron $1 main 65853af
	exit_stack_push git_checkout_undo $1
	ocm_application_samples_patch_old_undo $1
	ocm_application_samples_patch_apply $1
	exit_stack_push ocm_application_samples_patch_apply $1 --reverse
}
exit_stack_push unset -f ocm_application_samples_checkout
ocm_application_samples_checkout_undo()
{
	exit_stack_pop
	exit_stack_pop
}
exit_stack_push unset -f ocm_application_samples_checkout_undo
application_sample_deploy()
{
	kubectl --context ${hub_cluster_name} apply -k application-samples/subscriptions/channel
	kubectl --context ${hub_cluster_name} label managedclusters/${1} usage=test --overwrite
	kubectl --context ${hub_cluster_name} apply -k application-samples/subscriptions/book-import
	# https://github.com/kubernetes/kubernetes/issues/83242
	until_true_or_n 30 kubectl --context ${1} -n book-import wait deployments --all --for condition=available --timeout 0
}
application_sample_undeploy()
{
	kubectl --context ${hub_cluster_name} delete -k application-samples/subscriptions/book-import
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
	set -- $spoke_cluster_names_nonhub
	ocm_application_samples_checkout
	application_sample_deploy ${1}
	application_sample_undeploy ${1}
	ocm_application_samples_checkout_undo
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
for_each()
{
	for x in $1; do
		eval $2 $x
	done; unset -v x
}
exit_stack_push unset -f for_each
hub_kubeconfig_file_path_name=/tmp/$USER/$hub_cluster_name-config
exit_stack_push unset -v hub_kubeconfig_file_path_name
hub_kubeconfig_file_create()
{
	mkdir -p $(dirname $hub_kubeconfig_file_path_name)
	kubectl --context $hub_cluster_name config view --flatten --minify >$hub_kubeconfig_file_path_name
}
exit_stack_push unset -f hub_kubeconfig_file_create
deploy_hub()
{
	ocm_registration_operator_deploy_hub
}
exit_stack_push unset -f deploy_hub
undeploy_hub()
{
	ocm_registration_operator_undeploy_hub
}
exit_stack_push unset -f undeploy_hub
deploy_spokes()
{
	hub_kubeconfig_file_create
	for cluster_name in $spoke_cluster_names_hub   ; do spoke_add_hub    $cluster_name; done; unset -v cluster_name
	for cluster_name in $spoke_cluster_names_nonhub; do spoke_add_nonhub $cluster_name; done; unset -v cluster_name
}
exit_stack_push unset -f deploy_spokes
undeploy_spokes()
{
	for cluster_name in $spoke_cluster_names_nonhub $spoke_cluster_names_hub; do
		ocm_registration_operator_undeploy_spoke $cluster_name
	done; unset -v cluster_name
}
exit_stack_push unset -f undeploy_spokes
deploy()
{
	minikube_start_hub
	minikube_start_spokes
	deploy_hub
	deploy_spokes
	foundation_operator_deploy
	subscription_operator_deploy
	policy_operator_deploy
}
exit_stack_push unset -f deploy
undeploy()
{
	policy_operator_undeploy
	subscription_operator_undeploy
	foundation_operator_undeploy
	undeploy_spokes
	undeploy_hub
}
exit_stack_push unset -f undeploy
delete()
{
	for cluster_name in $spoke_cluster_names_nonhub $hub_cluster_name; do
		minikube delete -p $cluster_name
	done; unset -v cluster_name
}
exit_stack_push unset -f delete
exit_stack_push unset -v command
for command in "${@:-deploy}"; do
	$command
done
unset -v spoke_cluster_names_nonhub
unset -v spoke_cluster_names_hub
unset -v spoke_cluster_names
unset -v hub_cluster_name
unset -f application_sample_test
unset -f application_sample_undeploy
unset -f application_sample_deploy
unset -f spoke_add_nonhub
unset -f spoke_add_hub
unset -f application_sample_0_test
unset -f application_sample_0_undeploy
unset -f application_sample_0_deploy
unset -f until_true_or_n
unset -v minikube_start_options
