#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# shellcheck disable=1090,1091,2046,2086
set -e

# subshell ?
if test $(basename -- $0) = shio-demo.sh; then
	ramen_hack_directory_path_name=$(dirname -- $0)
else
	ramen_hack_directory_path_name=${ramen_hack_directory_path_name-hack}
	test -d "$ramen_hack_directory_path_name"
	shell_configure() {
		unset -f shell_configure
		exit_stack_push PS1=\'$PS1\'
		PS1='\[\033[01;32m\]$\[\033[00m\] '
		exit_stack_push PS4=\'$PS4\'
		PS4='
$ '
		set +e
	}
	set -- shell_configure
fi
. $ramen_hack_directory_path_name/exit_stack.sh
exit_stack_push unset -v ramen_hack_directory_path_name
. $ramen_hack_directory_path_name/minikube.sh; exit_stack_push minikube_unset
. $ramen_hack_directory_path_name/true_if_exit_status_and_stderr.sh; exit_stack_push unset -f true_if_exit_status_and_stderr

json_to_yaml() {
	python3 -c 'import sys, yaml, json; print(yaml.dump(json.loads(sys.stdin.read()),default_flow_style=False))'
}; exit_stack_push unset -f json_to_yaml

command_sequence() {
	cat <<-a
	#!/bin/sh

	# Deployed already: infrastructure
	infra_list

	# Deploy application
	app_deploy

	# Protect application
	app_protect
	s3_objects_list

	# Failover application from cluster1 to cluster2
	app_list cluster2
	app_failover

	# Failback application from cluster2 to cluster1
	app_list cluster1
	app_failback
	app_list cluster2
	a
}; exit_stack_push unset -f command_sequence

infra_deploy() {
	$ramen_hack_directory_path_name/minikube-ramen.sh deploy
	$ramen_hack_directory_path_name/velero-test.sh velero_deploy cluster1
	$ramen_hack_directory_path_name/velero-test.sh velero_deploy cluster2
	velero_secret_deploy cluster1
	velero_secret_deploy cluster2
	for cluster_name in $s3_store_cluster_names; do
		mc alias set $cluster_name $(minikube_minio_url $cluster_name) minio minio123
	done; unset -v cluster_name
	app_operator_deploy cluster1
	app_operator_deploy cluster2
	infra_list
}; exit_stack_push unset -f infra_deploy

infra_list() {
	set -x
	minikube profile list
	kubectl --context cluster1 --namespace ramen-system get deploy
	kubectl --context cluster2 --namespace ramen-system get deploy
	kubectl --context cluster1 --namespace velero get deploy/velero secret/s3secret
	kubectl --context cluster2 --namespace velero get deploy/velero secret/s3secret
	mc tree cluster1
	mc tree cluster2
	app_opperator_list cluster1
	app_opperator_list cluster2
	{ set +x; } 2>/dev/null
}; exit_stack_push unset -f infra_list

infra_undeploy() {
	app_operator_undeploy cluster2
	app_operator_undeploy cluster1
	velero_secret_undeploy cluster2
	velero_secret_undeploy cluster1
	$ramen_hack_directory_path_name/minikube-ramen.sh undeploy
}; exit_stack_push unset -f infra_undeploy

velero_secret_yaml() {
	kubectl create secret generic s3secret --from-literal aws='[default]
aws_access_key_id=minio
aws_secret_access_key=minio123
' --dry-run=client -oyaml --namespace velero
}; exit_stack_push unset -f velero_secret_yaml

velero_secret_deploy() {
	velero_secret_yaml|kubectl --context "$1" apply -f -
}; exit_stack_push unset -f velero_secret_deploy

velero_secret_undeploy() {
	velero_secret_yaml|kubectl --context "$1" delete --ignore-not-found -f -
}; exit_stack_push unset -f velero_secret_undeploy

velero_secret_list() {
	velero_secret_yaml|kubectl --context "$1" get -f -
}; exit_stack_push unset -f velero_secret_list

ns_list() {
	kubectl --context "$1" get namespace $2
}; exit_stack_push unset -f ns_list

ns_get() {
	kubectl --context "$1" get namespace $2 -oyaml
}; exit_stack_push unset -f ns_get

namespace_yaml() {
	cat <<-a
	---
	apiVersion: v1
	kind: Namespace
	metadata:
	  name: $1
	a
}; exit_stack_push unset -f namespace_yaml

app_operator_namespace_name=o
app_operator_recipe_name=r
exit_stack_push unset -v app_operator_namespace_name
exit_stack_push unset -v app_operator_recipe_name

app_operator_yaml() {
	namespace_yaml $app_operator_namespace_name
	app_operator_recipe_yaml
}; exit_stack_push unset -f app_operator_yaml

app_operator_deploy() {
	app_operator_yaml|kubectl --context "$1" apply -f -
}; exit_stack_push unset -f app_operator_deploy

app_operator_undeploy() {
	app_operator_yaml|kubectl --context "$1" delete --ignore-not-found -f -
}; exit_stack_push unset -f app_operator_undeploy

app_operator_list() {
	app_operator_yaml|kubectl --context "$1" get -f -
}; exit_stack_push unset -f app_operator_list

app_namespace_0_name=a
app_namespace_1_name=asdf
app_namespace_2_name=b
app_namespace_names=$app_namespace_0_name\ $app_namespace_1_name\ $app_namespace_2_name
exit_stack_push unset -f app_namespace_0_name app_namespace_1_name app_namespace_2_name app_namespace_names

app_label_key=appname
app_label_value=busybox
app_label=$app_label_key=$app_label_value
app_label_yaml=$app_label_key:\ $app_label_value
app_labels_yaml="\
  labels:
    $app_label_yaml"
exit_stack_push unset -v app_label_key app_label_value app_label app_label_yaml app_labels_yaml

app_namespace_yaml() {
	namespace_yaml $1
	echo "$app_labels_yaml"
}; exit_stack_push unset -f app_namespace_yaml

app_namespaces_yaml() {
	for namespace_name in $app_namespace_names; do
		app_namespace_yaml $namespace_name
	done; unset -v namespace_name
}; exit_stack_push unset -f app_namespaces_yaml

app_namespaces_deploy() {
	app_namespaces_yaml|kubectl --context "$1" apply -f -
}; exit_stack_push unset -f app_namespaces_deploy

app_namespaces_undeploy() {
	app_namespaces_yaml|kubectl --context "$1" delete --ignore-not-found -f -
}; exit_stack_push unset -f app_namespaces_undeploy

app_naked_pod_name=busybox
app_clothed_pod_name=busybox
app_configmap_name=asdf
app_secret_name=$app_configmap_name
exit_stack_push unset -v app_naked_pod_name app_clothed_pod_name app_configmap_name app_secret_name

app_namespaced_yaml() {
	echo ---
	kubectl --dry-run=client -oyaml --namespace "$1" create -k https://github.com/RamenDR/ocm-ramen-samples/busybox
	echo ---
	kubectl --dry-run=client -oyaml --namespace "$1" create configmap $app_configmap_name
	echo "$app_labels_yaml"
	echo ---
	kubectl --dry-run=client -oyaml --namespace "$1" create secret generic $app_secret_name --from-literal=key1=value1
	echo "$app_labels_yaml"
	echo ---
	kubectl --dry-run=client -oyaml --namespace "$1" run $app_naked_pod_name -l$app_label --image busybox -- sh -c while\ true\;do\ date\;sleep\ 60\;done
}; exit_stack_push unset -v app_namespaced_yaml

app_less_namespaces_yaml() {
	for namespace_name in $app_namespace_names; do
		app_namespaced_yaml $namespace_name
	done; unset -v namespace_name
}; exit_stack_push unset -v app_less_namespaces_yaml

app_yaml() {
	app_namespaces_yaml
	app_less_namespaces_yaml
}; exit_stack_push unset -v app_yaml

app_deploy() {
	set -- cluster1
	app_yaml|kubectl --context "$1" apply -f -
	app_pvs_label "$1"
	app_list "$1"
}; exit_stack_push unset -f app_deploy

app_pvs_label() {
	for namespace_name in $app_namespace_names; do
		kubectl --context "$1" --namespace $namespace_name -l$app_label wait pvc --for jsonpath='{.status.phase}'=Bound
		kubectl --context "$1" label $(pv_names_claimed_by_namespace "$1" $namespace_name) $app_label --overwrite
	done; unset -v namespace_name
}; exit_stack_push unset -f app_pvs_label

app_less_namespaces_undeploy() {
	app_less_namespaces_yaml|kubectl --context "$1" delete --ignore-not-found -f -
	app_list $1
}; exit_stack_push unset -f app_less_namespaces_undeploy

app_undeploy() {
	app_yaml|kubectl --context "$1" delete --ignore-not-found -f -
	app_list $1
}; exit_stack_push unset -f app_undeploy

app_operator_recipe_yaml() {
	cat <<-a
	---
	apiVersion: ramendr.openshift.io/v1alpha1
	kind: Recipe
	metadata:
	  namespace: $app_operator_namespace_name
	  name: $app_operator_recipe_name
	spec:
	  appType: ""
	  volumes:
	    includedNamespaces:
	    - \$ns0
	    - \$ns1_2
	    name: ""
	    type: volume
	  groups:
	  - includedNamespaces:
	    - \$ns0
	    - \$ns1_2
	    name: ""
	    type: resource
	  - excludedResourceTypes:
	    - deploy
	    - po
	    - pv
	    - rs
	    - volumereplications
	    - vrg
	    name: everything-but-deploy-po-pv-rs-vr-vrg
	    type: resource
	  - includedResourceTypes:
	    - deployments
	    - pods
	    labelSelector:
	      matchExpressions:
	      - key: pod-template-hash
	        operator: DoesNotExist
	    name: deployments-and-naked-pods
	    type: resource
	  hooks:
	  - name: busybox1
	    namespace: \$ns1
	    type: exec
	    labelSelector:
	      matchExpressions:
	      - key: pod-template-hash
	        operator: Exists
	    ops:
	    - name: date
	      container: $app_clothed_pod_name
	      command:
	      - date
	  - name: busybox0
	    namespace: \$ns0
	    type: exec
	    labelSelector:
	      matchExpressions:
	      - key: pod-template-hash
	        operator: DoesNotExist
	    ops:
	    - name: fail-succeed
	      container: $app_naked_pod_name
	      command:
	      - sh
	      - -c
	      - "rm /tmp/a||! touch /tmp/a"
	  captureWorkflow:
	    sequence:
	    - group: ""
	  recoverWorkflow:
	    sequence:
	    - group: everything-but-deploy-po-pv-rs-vr-vrg
	    - group: deployments-and-naked-pods
	    - hook: busybox0/fail-succeed
	a
# TODO restore once PR 871 is merged
#	    - hook: busybox1/date
}; exit_stack_push unset -f app_operator_recipe_yaml

app_operator_recipe_get() {
	kubectl --context "$1" --namespace $app_operator_namespace_name get -oyaml recipe/$app_operator_recipe_name
}; exit_stack_push unset -f app_operator_recipe_get

app_list() {
	app_list_custom "$1" --show-labels
	echo
	app_list_custom "$1" --sort-by=.metadata.creationTimestamp\
\	-ocustom-columns=Kind:.kind,Namespace:.metadata.namespace,Name:.metadata.name,CreationTime:.metadata.creationTimestamp\

}; exit_stack_push unset -f app_list

app_list_custom() {
	kubectl --context "$1" -A -l$app_label get ns,cm,secret,deploy,rs,po,pvc,pv,recipe,vrg,vr $2
}; exit_stack_push unset -f app_list_custom

vrg_namespace_name=ramen-system
exit_stack_push unset -v vrg_namespace_name

vrg_apply() {
	vrg_appendix="
  kubeObjectProtection:
    captureInterval: 1m
    recipeRef:
      namespace: $app_operator_namespace_name
      name: $app_operator_recipe_name
    recipeParameters:
      ns0:
      - $app_namespace_0_name
      ns1:
      - $app_namespace_1_name
      ns1_2:
      - $app_namespace_1_name
      - $app_namespace_2_name$3${4:+
  action: $4}"\
	cluster_names=$s3_store_cluster_names\
	$ramen_hack_directory_path_name/minikube-ramen.sh application_sample_vrg_deploy$2 "$1" "$vrg_namespace_name" "$app_label_yaml"
}; exit_stack_push unset -f vrg_apply

vrg_deploy() {
	vrg_apply "$1" "$2" "$3" $4
	vrg_list "$1"
}; exit_stack_push unset -f vrg_deploy

vrg_deploy_failover() {
	vrg_deploy "$1" "$2" "$3" Failover
}; exit_stack_push unset -f vrg_deploy_failover

vrg_deploy_relocate() {
	vrg_deploy "$1" "$2" "$3" Relocate
}; exit_stack_push unset -f vrg_deploy_relocate

vrg_undeploy() {
	cluster_names=$s3_store_cluster_names $ramen_hack_directory_path_name/minikube-ramen.sh application_sample_vrg_undeploy "$1" "$vrg_namespace_name"
}; exit_stack_push unset -f vrg_undeploy

vrg_demote() {
	vrg_deploy_$2 "$1" _sec
#	time kubectl --context "$1" --namespace "$vrg_namespace_name" wait vrg/bb --for condition=clusterdataprotected=false
}; exit_stack_push unset -f vrg_demote

vrg_final_sync() {
	vrg_apply $1 '' '
  prepareForFinalSync: true'
	time kubectl --context "$1" --namespace "$vrg_namespace_name" wait vrg/bb --for jsonpath='{.status.prepareForFinalSyncComplete}'=true
	vrg_apply $1 '' '
  runFinalSync: true'
	time kubectl --context "$1" --namespace "$vrg_namespace_name" wait vrg/bb --for jsonpath='{.status.finalSyncComplete}'=true
}; exit_stack_push unset -f vrg_final_sync

vrg_fence() {
	vrg_demote "$1" failover
}; exit_stack_push unset -f vrg_fence

vrg_finalizer0_remove() {
	true_if_exit_status_and_stderr 1 'Error from server (NotFound): volumereplicationgroups.ramendr.openshift.io "bb" not found' \
	kubectl --context "$1" --namespace "$vrg_namespace_name" patch vrg/bb --type json -p '[{"op":remove, "path":/metadata/finalizers/0}]'
}; exit_stack_push unset -f vrg_finalizer0_remove

vr_finalizer0_remove() {
	true_if_exit_status_and_stderr 1 'Error from server (NotFound): volumereplications.replication.storage.openshift.io "busybox-pvc" not found' \
	kubectl --context "$1" --namespace "$vrg_namespace_name" patch volumereplication/busybox-pvc --type json -p '[{"op":remove, "path":/metadata/finalizers/0}]'
}; exit_stack_push unset -f vr_finalizer0_remove

vrg_get() {
	kubectl --context "$1" --namespace "$vrg_namespace_name" get vrg/bb --ignore-not-found -oyaml
}; exit_stack_push unset -f vrg_get

vrg_spec_get() {
	kubectl --context "$1" --namespace "$vrg_namespace_name" get vrg/bb -ojsonpath='{.spec}'|jq
}; exit_stack_push unset -f vrg_spec_get

vrg_conditions_get() {
	kubectl --context "$1" --namespace "$vrg_namespace_name" get vrg/bb -ojsonpath='{.status.conditions}'|jq
}; exit_stack_push unset -f vrg_conditions_get

vrg_list() {
	set -x
	kubectl --context "$1" --namespace "$vrg_namespace_name" get vrg/bb --ignore-not-found
	{ set +x;} 2>/dev/null
}; exit_stack_push unset -f vrg_list

vrg_get_s3() {
	mc cp -q $(app_s3_object_name_prefix $1)v1alpha1.VolumeReplicationGroup/a /tmp/a.json.gz;gzip -df /tmp/a.json.gz;json_to_yaml </tmp/a.json
}; exit_stack_push unset -f vrg_get_s3

vrg_primary_status_wait() {
	set -x
	time kubectl --context "$1" --namespace "$vrg_namespace_name" wait vrg/bb --timeout -1s --for condition=clusterdataready
	time kubectl --context "$1" --namespace "$vrg_namespace_name" wait vrg/bb --timeout -1s --for condition=clusterdataprotected
	time kubectl --context "$1" --namespace "$vrg_namespace_name" wait vrg/bb --timeout -1s --for condition=dataready
	time kubectl --context "$1" --namespace "$vrg_namespace_name" wait vrg/bb --timeout -1s --for jsonpath='{.status.state}'=Primary
#	time kubectl --context "$1" --namespace "$vrg_namespace_name" wait vrg/bb --timeout -1s --for jsonpath='{.status.conditions[?(@.type=="DataProtected")].reason}'=Replicating
	time kubectl --context "$1" --namespace "$vrg_namespace_name" wait vrg/bb --timeout -1s --for jsonpath='{.status.conditions[1].reason}'=Replicating
	{ set +x; } 2>/dev/null
	vr_label
}; exit_stack_push unset -f vrg_primary_status_wait

vr_label() {
	for namespace_name in $app_namespace_names; do
		set -x
		kubectl --context "$1" --namespace "$namespace_name" label vr/busybox-pvc "$app_label" --overwrite
		{ set +x;} 2>/dev/null
	done; unset -v namespace_name
}; exit_stack_push unset -f vr_label

vr_get() {
	kubectl --context "$1" --selector "$app_label" get volumereplication/busybox-pvc --ignore-not-found -oyaml
}; exit_stack_push unset -f vr_get

vr_list() {
	kubectl --context "$1" --selector "$app_label" get volumereplication/busybox-pvc --ignore-not-found
}; exit_stack_push unset -f vr_list

vr_delete() {
	kubectl --context "$1" --selector "$app_label" delete volumereplication/busybox-pvc --ignore-not-found
}; exit_stack_push unset -f vr_delete

pvc_get() {
	kubectl --context "$1" --selector "$app_label" get pvc/busybox-pvc --ignore-not-found -oyaml
}; exit_stack_push unset -f pvc_get

pv_names_claimed_by_namespace() {
	kubectl --context "$1" get pv -ojsonpath='{range .items[?(@.spec.claimRef.namespace=="'$2'")]} pv/{.metadata.name}{end}'
}; exit_stack_push unset -f pv_names_claimed_by_namespace

pv_names() {
	kubectl --context "$1" get pv -ojsonpath='{range .items[?(@.spec.claimRef.name=="busybox-pvc")]} pv/{.metadata.name}{end}'
}; exit_stack_push unset -f pv_names

pv_list() {
	kubectl --context "$1" get $(pv_names $1) --show-kind
}; exit_stack_push unset -f pv_list

pv_get() {
	kubectl --context "$1" get $(pv_names $1) -oyaml
}; exit_stack_push unset -f pv_get

pv_delete() {
	kubectl --context "$1" delete $(pv_names $1)
}; exit_stack_push unset -f pv_delete

pv_unretain() {
	kubectl --context "$1" patch $(pv_names $1) --type json -p '[{"op":add, "path":/spec/persistentVolumeReclaimPolicy, "value":Delete}]'
}; exit_stack_push unset -f pv_unretain

app_protect() {
	set -- cluster1
	vrg_deploy $1
	vrg_primary_status_wait $1
#	app_protection_info 1
}; exit_stack_push unset -f app_protect

app_unprotect() {
	vrg_undeploy $1
	kubectl --context "$1" --namespace "$vrg_namespace_name" delete events --all
	velero_kube_objects_list $1
	s3_objects_list
}; exit_stack_push unset -f app_unprotect

app_failover() {
	set -- cluster1 cluster2
	vrg_fence $1
	app_recover $2 failover
}; exit_stack_push unset -f app_failover

app_failback() {
	set -- cluster1 cluster2
	app_undeploy_failback $1 failover
#	vrg_final_sync $2
	app_undeploy_failback $2 relocate app_recover_failback\ $1\ $2
}; exit_stack_push unset -f app_failback

app_recover() {
	app_namespaces_deploy $1
	vrg_deploy_$2 $1
	vrg_primary_status_wait $1
	app_list $1
}; exit_stack_push unset -f app_recover

app_undeploy_failback() {
	vrg_demote $1 $2
	# "PVC not being deleted. Not ready to become Secondary"
	set -x
	time kubectl --context "$1" --namespace "$vrg_namespace_name" wait vrg/bb --timeout -1s --for condition=clusterdataprotected
	{ set +x; } 2>/dev/null
	time app_less_namespaces_undeploy $1& # pvc finalizer remains until vrg deletes its vr
	set -x
	time kubectl --context "$1" --namespace "$vrg_namespace_name" wait vrg/bb --timeout -1s --for jsonpath='{.status.state}'=Secondary
	{ set +x; } 2>/dev/null
	$3
	vrg_undeploy $1&
	time wait
	time app_namespaces_undeploy $1
}; exit_stack_push unset -f app_undeploy_failback

app_recover_failback() {
	# "VolumeReplication resource for the pvc as Secondary is in sync with Primary"
	set -x
	time kubectl --context "$2" --namespace "$vrg_namespace_name" wait vrg/bb --timeout -1s --for condition=dataprotected
	{ set +x; } 2>/dev/null
	app_recover "$1" relocate
}; exit_stack_push unset -f app_recover_failback

app_velero_kube_object_name=$vrg_namespace_name--bb--
exit_stack_push unset -v app_velero_kube_object_name

s3_objects_list() {
	for cluster_name in $s3_store_cluster_names; do
		mc tree $cluster_name
		mc ls $cluster_name --recursive
	done; unset -v cluster_name
}; exit_stack_push unset -f s3_objects_list

s3_objects_delete() {
	for cluster_name in $s3_store_cluster_names; do
		mc rm $cluster_name/bucket/ --recursive --force\
		||true # https://github.com/minio/mc/issues/3868
	done; unset -v cluster_name
}; exit_stack_push unset -f s3_objects_list

app_s3_object_name_prefix() {
	echo $1/bucket/$vrg_namespace_name/bb/
}; exit_stack_push unset -f app_s3_object_name_prefix

app_s3_object_name_prefix_velero() {
	echo $(app_s3_object_name_prefix $2)kube-objects/$1/velero/
}; exit_stack_push unset -f app_s3_object_name_prefix_velero

app_s3_objects_delete() {
	for cluster_name in $s3_store_cluster_names; do
		mc rm $(app_s3_object_name_prefix $cluster_name) --recursive --force\
		||true # https://github.com/minio/mc/issues/3868
	done; unset -v cluster_name
}; exit_stack_push unset -f app_objects_delete

app_protection_info() {
	for cluster_name in $s3_store_cluster_names; do
		set -- "$1" $(app_s3_object_name_prefix_velero "$1" $cluster_name) $app_velero_kube_object_name$1----minio-on-$cluster_name
		velero_backup_log $2 $3
		velero_backup_backup_object $2 $3
		velero_backup_resource_list $2 $3
	done; unset -v cluster_name
}; exit_stack_push unset -f app_protection_info

app_recovery_info() {
	for cluster_name in $s3_store_cluster_names; do
		set -- "$1" "$2" $(app_s3_object_name_prefix_velero "$1" $cluster_name) $app_velero_kube_object_name$2
		velero_restore_log $3 $4
		velero_restore_results $3 $4
	done; unset -v cluster_name
}; exit_stack_push unset -f app_recovery_info

velero_backup_backup_object() {
	mc cp -q $1backups/$2/velero-backup.json /tmp/$2-velero-backup.json;json_to_yaml </tmp/$2-velero-backup.json
}; exit_stack_push unset -f velero_backup_backup_object

velero_backup_resource_list() {
	mc cp -q $1backups/$2/$2-resource-list.json.gz /tmp;gzip -df /tmp/$2-resource-list.json.gz;json_to_yaml </tmp/$2-resource-list.json
}; exit_stack_push unset -f velero_backup_resource_list

velero_backup_log() {
	mc cp -q $1backups/$2/$2-logs.gz /tmp;gzip -df /tmp/$2-logs.gz;cat /tmp/$2-logs
}; exit_stack_push unset -f velero_backup_log

velero_restore_results() {
	mc cp -q $1restores/$2/restore-$2-results.gz /tmp;gzip -df /tmp/restore-$2-results.gz;json_to_yaml </tmp/restore-$2-results
}; exit_stack_push unset -f velero_restore_results

velero_restore_log() {
	mc cp -q $1restores/$2/restore-$2-logs.gz /tmp;gzip -df /tmp/restore-$2-logs.gz;cat /tmp/restore-$2-logs
}; exit_stack_push unset -f velero_restore_log

velero_kube_objects_list() {
	velero --kubecontext "$1" get backups
	velero --kubecontext "$1" get backup-locations
	velero --kubecontext "$1" get restores
}; exit_stack_push unset -f velero_kube_objects_list

velero_kube_objects_undeploy() {
	velero_kube_objects_list $1
	velero --kubecontext "$1" delete --all --confirm backups
	velero --kubecontext "$1" delete --all --confirm backup-locations
	velero --kubecontext "$1" delete --all --confirm restores
	velero_kube_objects_list $1
}; exit_stack_push unset -f velero_kube_objects_undeploy

velero_kube_objects_delete() {
	kubectl --context "$1" --namespace velero delete --all restores,backups,backupstoragelocations
}; exit_stack_push unset -f velero_kube_objects_delete

s3_store_cluster_names=${s3_store_cluster_names-cluster2\ cluster1}
exit_stack_push unset -v s3_store_cluster_names

"$@"
