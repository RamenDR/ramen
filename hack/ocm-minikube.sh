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

ocm_registration_operator_patch_old_undo()
{
	git_checkout $1 --\ Makefile
}
exit_stack_push unset -f ocm_registration_operator_patch_old_undo
ocm_registration_operator_patch_apply()
{
	cat <<'a' | git apply --directory $1 $2 -
diff --git a/Makefile b/Makefile
index fcf9775..58d7a44 100644
--- a/Makefile
+++ b/Makefile
@@ -118,7 +118,7 @@ deploy-spoke-operator: ensure-kustomize
 	$(KUSTOMIZE) build deploy/klusterlet/config | $(KUBECTL) apply -f -
 
 apply-spoke-cr: bootstrap-secret
-	$(KUSTOMIZE) build deploy/klusterlet/config/samples | $(SED_CMD) -e "s,quay.io/open-cluster-management/registration,$(REGISTRATION_IMAGE)," -e "s,quay.io/open-cluster-management/work,$(WORK_IMAGE)," | $(KUBECTL) apply -f -
+	$(KUSTOMIZE) build deploy/klusterlet/config/samples | $(SED_CMD) -e "s,cluster1,$(MANAGED_CLUSTER)," -e "s,quay.io/open-cluster-management/registration,$(REGISTRATION_IMAGE)," -e "s,quay.io/open-cluster-management/work,$(WORK_IMAGE)," | $(KUBECTL) apply -f -
 
 clean-hub-cr:
 	$(KUBECTL) delete -k deploy/cluster-manager/config/samples --ignore-not-found
a
}
exit_stack_push unset -f ocm_registration_operator_patch_apply
ocm_registration_operator_checkout()
{
	set -- registration-operator
	git_clone_and_checkout https://github.com/open-cluster-management $1 release-2.3 c723e19
	exit_stack_push git_checkout_undo $1
	ocm_registration_operator_patch_old_undo $1
	ocm_registration_operator_patch_apply $1
	exit_stack_push ocm_registration_operator_patch_apply $1 --reverse
}
exit_stack_push unset -f ocm_registration_operator_checkout
ocm_registration_operator_checkout_undo()
{
	exit_stack_pop
	exit_stack_pop
}
exit_stack_push unset -f ocm_registration_operator_checkout_undo
case ${NAME} in
"Ubuntu")
	ocm_registration_operator_make_arguments=GO_REQUIRED_MIN_VERSION:=
	;;
esac
ocm_registration_operator_deploy_hub()
{
	kubectl --context ${hub_cluster_name} apply -f https://raw.githubusercontent.com/kubernetes/cluster-registry/master/cluster-registry-crd.yaml
	kubectl config use-context ${hub_cluster_name}
	ocm_registration_operator_checkout
	make -C registration-operator deploy-hub $ocm_registration_operator_make_arguments
	ocm_registration_operator_checkout_undo
	date
	kubectl --context $hub_cluster_name -n open-cluster-management wait deployments/cluster-manager --for condition=available
	date
	# https://github.com/kubernetes/kubernetes/issues/83242
	until_true_or_n 90 kubectl --context ${hub_cluster_name} -n open-cluster-management-hub wait deployments --all --for condition=available --timeout 0
}
ocm_registration_operator_undeploy_hub()
{
	kubectl config use-context ${hub_cluster_name}
	ocm_registration_operator_checkout
	set +e
	make -C registration-operator clean-hub-cr clean-hub-operator $ocm_registration_operator_make_arguments
	# error: unable to recognize "deploy/cluster-manager/config/samples/operator_open-cluster-management_clustermanagers.cr.yaml": no matches for kind "ClusterManager" in version "operator.open-cluster-management.io/v1"
	set -e
	ocm_registration_operator_checkout_undo
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
ocm_registration_operator_deploy_spoke()
{
	kubectl config use-context ${1}
	ocm_registration_operator_checkout
	MANAGED_CLUSTER=${1}\
	HUB_KUBECONFIG=${HUB_KUBECONFIG}\
	make -C registration-operator deploy-spoke ${ocm_registration_operator_make_arguments}
	ocm_registration_operator_checkout_undo
	date
	kubectl --context $1 -n open-cluster-management wait deployments/klusterlet --for condition=available
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
	kubectl config use-context ${1}
	ocm_registration_operator_checkout
	set +e
	make -C registration-operator clean-spoke-cr clean-spoke-operator $ocm_registration_operator_make_arguments
	# error: unable to recognize "deploy/klusterlet/config/samples/operator_open-cluster-management_klusterlets.cr.yaml": no matches for kind "Klusterlet" in version "operator.open-cluster-management.io/v1"
	set -e
	ocm_registration_operator_checkout_undo
}
ocm_foundation_operator_kustomization_directory()
{
	echo https://github.com/open-cluster-management/multicloud-operators-foundation/deploy/foundation/$1?ref=b5df50deb87618e8c6057637705e94a58b5a19da
}
exit_stack_push unset -f ocm_foundation_operator_kustomization_directory
ocm_foundation_operator_kubectl_hub()
{
	kubectl --context $hub_cluster_name $1 -k $(ocm_foundation_operator_kustomization_directory hub)
}
exit_stack_push unset -f ocm_foundation_operator_kubectl_hub
ocm_foundation_operator_kubectl_spoke()
{
	set -- $1 $2 /tmp/$USER/open-cluster-management/foundation/klusterlet/$1
	mkdir -p $3
	cat <<-a >$3/kustomization.yaml
	resources:
	  - $(ocm_foundation_operator_kustomization_directory klusterlet)
	namespace: $1
	patchesJson6902:
	  - target:
	      group: work.open-cluster-management.io
	      version: v1
	      kind: ManifestWork
	      name: klusterlet-addon-workmgr
	    patch: |-
	      - op: test
	        path: /spec/workload/manifests/4/spec/template/spec/containers/0/args/2
	        value: --cluster-name=cluster1
	      - op: replace
	        path: /spec/workload/manifests/4/spec/template/spec/containers/0/args/2
	        value: --cluster-name=$1
	a
	kubectl --context $hub_cluster_name $2 -k $3
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
	ocm_foundation_operator_kubectl_hub delete
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
ocm_subscription_operator_git_ref=c48c55969bc4385bc694acd8bc92e5bf4e0181d3
exit_stack_push unset -v ocm_subscription_operator_git_ref
ocm_subscription_operator_checkout()
{
	set -- multicloud-operators-subscription
	git_clone_and_checkout https://github.com/open-cluster-management $1 main $ocm_subscription_operator_git_ref
	exit_stack_push git_checkout_undo $1
}
exit_stack_push unset -f ocm_subscription_operator_checkout
ocm_subscription_operator_checkout_undo()
{
	exit_stack_pop
}
exit_stack_push unset -f ocm_subscription_operator_checkout_undo
ocm_subscription_operator_deploy_hub()
{
	kubectl config use-context ${hub_cluster_name}
	ocm_subscription_operator_checkout
	USE_VENDORIZED_BUILD_HARNESS=faked make -C multicloud-operators-subscription deploy-community-hub
	ocm_subscription_operator_checkout_undo
	date
	kubectl --context ${hub_cluster_name} -n multicluster-operators wait deployments --all --for condition=available --timeout 2m
	date
}
exit_stack_push unset -f ocm_subscription_operator_deploy_hub
ocm_subscription_operator_deploy_spoke()
{
	set -- $1 open-cluster-management subscription managed "$2"
	set -- $1 $2 multicloud-operators-$3 $4 /tmp/$USER/$2/$3/$4/$1 "$5"
	# https://github.com/open-cluster-management-io/multicloud-operators-subscription/issues/16
	kubectl --context $hub_cluster_name label managedclusters/$1 name=$1 --overwrite
	ocm_subscription_operator_checkout
	kubectl --context $1 apply -f $3/deploy/common
	ocm_subscription_operator_checkout_undo
	cp -f $HUB_KUBECONFIG /tmp/$USER/kubeconfig
	kubectl --context $1 -n multicluster-operators delete secret appmgr-hub-kubeconfig --ignore-not-found
	kubectl --context $1 -n multicluster-operators create secret generic appmgr-hub-kubeconfig --from-file=kubeconfig=/tmp/$USER/kubeconfig
	mkdir -p $5
	cat <<-a >$5/kustomization.yaml
	resources:
	  - https://raw.githubusercontent.com/$2/$3/$ocm_subscription_operator_git_ref/deploy/$4/operator.yaml
	patchesJson6902:
	  - target:
	      group: apps
	      version: v1
	      kind: Deployment
	      name: multicluster-operators-subscription
	      namespace: multicluster-operators
	    patch: |-
	      - op: test
	        path: /spec/template/spec/containers/0/command/2
	        value: --cluster-name=<managed cluster name>
	      - op: replace
	        path: /spec/template/spec/containers/0/command/2
	        value: --cluster-name=$1
	      - op: test
	        path: /spec/template/spec/containers/0/command/3
	        value: --cluster-namespace=<managed cluster namespace>
	      - op: replace
	        path: /spec/template/spec/containers/0/command/3
	        value: --cluster-namespace=$1
	      - op: test
	        path: /metadata/name
	        value: multicluster-operators-subscription
	      - op: replace
	        path: /metadata/name
	        value: multicluster-operators-subscription$6
	a
	kubectl --context $1 apply -k $5
	date
	kubectl --context $1 -n multicluster-operators wait deployments --all --for condition=available --timeout 1m
	date
}
exit_stack_push unset -f ocm_subscription_operator_deploy_spoke
ocm_subscription_operator_undeploy_spoke()
{
	kubectl --context $hub_cluster_name label managedclusters/$1 name-
}
exit_stack_push unset -f ocm_subscription_operator_undeploy_spoke
ocm_subscription_operator_deploy_spoke_hub()
{
	ocm_subscription_operator_deploy_spoke $1 '-mc'
}
exit_stack_push unset -f ocm_subscription_operator_deploy_spoke_hub
ocm_subscription_operator_deploy_spoke_nonhub()
{
	ocm_subscription_operator_deploy_spoke $1 ''
}
exit_stack_push unset -f ocm_subscription_operator_deploy_spoke_nonhub
spoke_test_deploy()
{
	ocm_subscription_operator_checkout
	kubectl --context ${hub_cluster_name} apply -f multicloud-operators-subscription/examples/helmrepo-hub-channel
	ocm_subscription_operator_checkout_undo
	# https://github.com/kubernetes/kubernetes/issues/83242
	until_true_or_n 60 kubectl --context ${1} wait deployments --selector app=nginx-ingress --for condition=available --timeout 0
}
spoke_test_undeploy()
{
	ocm_subscription_operator_checkout
	kubectl --context ${hub_cluster_name} delete -f multicloud-operators-subscription/examples/helmrepo-hub-channel
	ocm_subscription_operator_checkout_undo
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
spoke_add()
{
	ocm_registration_operator_deploy_spoke $1
	ocm_foundation_operator_deploy_spoke $1
}
exit_stack_push unset -f spoke_add
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
	git_clone_and_checkout https://github.com/open-cluster-management $1 main 65853af
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
for command in "${@:-deploy}"; do
        case ${command} in
        deploy)
		minikube start ${minikube_start_options} --profile=${hub_cluster_name} --cpus=4
		ocm_registration_operator_deploy_hub
		ocm_foundation_operator_deploy_hub
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
	foundation_operator_deploy)
		ocm_foundation_operator_deploy_hub
		for_each "$spoke_cluster_names" ocm_foundation_operator_deploy_spoke
		;;
	foundation_operator_undeploy)
		for_each "$spoke_cluster_names" ocm_foundation_operator_undeploy_spoke
		ocm_foundation_operator_undeploy_hub
		;;
	undeploy)
		for cluster_name in ${spoke_cluster_names_nonhub} ${spoke_cluster_names_hub}; do
			ocm_subscription_operator_undeploy_spoke $cluster_name
			ocm_foundation_operator_undeploy_spoke $cluster_name
			ocm_registration_operator_undeploy_spoke ${cluster_name}
		done; unset -v cluster_name
		ocm_foundation_operator_undeploy_hub
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
unset -f spoke_add_nonhub
unset -f spoke_add_hub
unset -f spoke_test
unset -f spoke_test_undeploy
unset -f spoke_test_deploy
unset -f application_sample_0_test
unset -f application_sample_0_undeploy
unset -f application_sample_0_deploy
unset -f ocm_registration_operator_undeploy_spoke
unset -f ocm_registration_operator_undeploy_hub
unset -f ocm_registration_operator_deploy_hub
unset -v ocm_registration_operator_make_arguments
unset -f until_true_or_n
unset -v minikube_start_options
git_branch_delete_force ocm-minikube shyam-main
git_branch_delete registration-operator release-2.3
