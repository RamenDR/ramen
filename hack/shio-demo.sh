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
. $ramen_hack_directory_path_name/until_true_or_n.sh; exit_stack_push unset -f until_true_or_n

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
	infra_list
}; exit_stack_push unset -f infra_deploy

infra_list() {
	set -x
	minikube profile list
	kubectl --context cluster1 -nramen-system get deploy
	kubectl --context cluster2 -nramen-system get deploy
	kubectl --context cluster1 -nvelero get deploy/velero secret/s3secret
	kubectl --context cluster2 -nvelero get deploy/velero secret/s3secret
	mc tree cluster1
	mc tree cluster2
	{ set +x; } 2>/dev/null
}; exit_stack_push unset -f infra_list

infra_undeploy() {
	velero_secret_undeploy cluster2
	velero_secret_undeploy cluster1
	$ramen_hack_directory_path_name/minikube-ramen.sh undeploy
}; exit_stack_push unset -f infra_undeploy

velero_secret_kubectl() {
	kubectl create secret generic s3secret --from-literal aws='[default]
aws_access_key_id=minio
aws_secret_access_key=minio123
' --dry-run=client -oyaml|kubectl --context $1 -nvelero $2 -f-
}; exit_stack_push unset -f velero_secret_kubectl

velero_secret_deploy() {
	velero_secret_kubectl $1 apply
}; exit_stack_push unset -f velero_secret_deploy

velero_secret_undeploy() {
	velero_secret_kubectl $1 delete\ --ignore-not-found
}; exit_stack_push unset -f velero_secret_undeploy

velero_secret_list() {
	kubectl --context $1 -nvelero get secret s3secret
}; exit_stack_push unset -f velero_secret_list

app_namespace_deploy() {
	set -x
	kubectl create namespace asdf --dry-run=client -oyaml|kubectl --context $1 apply -f-
	{ set +x;} 2>/dev/null
}; exit_stack_push unset -f app_namespace_deploy

app_namespace_undeploy() {
	set -x
	kubectl --context $1 delete namespace asdf --ignore-not-found
	{ set +x;} 2>/dev/null
}; exit_stack_push unset -f app_namespace_undeploy

app_namespace_list() {
	kubectl --context $1 get namespace asdf
}; exit_stack_push unset -f app_namespace_list

app_namespace_get() {
	kubectl --context $1 get namespace asdf -oyaml
}; exit_stack_push unset -f app_namespace_get

app_deploy() {
	set -- cluster1
	app_namespace_deploy $1
	kubectl create --dry-run=client -oyaml configmap asdf\
		|kubectl --context $1 -nasdf apply -f-
	kubectl create --dry-run=client -oyaml secret generic asdf --from-literal=key1=value1\
		|kubectl --context $1 -nasdf apply -f-
	kubectl --context $1 -nasdf run asdf --image busybox -- sh -c while\ true\;do\ date\;sleep\ 60\;done
	kubectl create --dry-run=client -oyaml -k https://github.com/RamenDR/ocm-ramen-samples/busybox -nasdf\
		|kubectl --context $1 apply -f-
	app_recipe_deploy $1
	app_list $1
}; exit_stack_push unset -f app_deploy

app_recipe_deploy() {
	cat <<-a|kubectl --context $1 -nasdf apply -f-
	apiVersion: ramendr.openshift.io/v1alpha1
	kind: Recipe
	metadata:
	  name: asdf
	spec:
	  appType: ""
	  groups:
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
	  - name: busybox
	    type: exec
	    labelSelector:
	      matchLabels:
	        appname: busybox
	    ops:
	    - name: date
	      container: busybox
	      command:
	      - date
	    - name: fail-succeed
	      container: busybox
	      command:
	      - sh
	      - -c
	      - "rm /tmp/a||! touch /tmp/a"
	  recoverWorkflow:
	    sequence:
	    - group: everything-but-deploy-po-pv-rs-vr-vrg
	    - group: deployments-and-naked-pods
	    - hook: busybox/date
	    - hook: busybox/fail-succeed
	a
}; exit_stack_push unset -f app_recipe_deploy

app_recipe_get() {
	kubectl --context $1 -nasdf get -oyaml recipe/asdf
}; exit_stack_push unset -f app_recipe_get

app_deployment_replicaset_name() {
	kubectl --context $1 -nasdf get rs -lpod-template-hash -oname
}; exit_stack_push unset -f app_deployment_replicaset_name

app_replicaset_pod_name() {
	kubectl --context $1 -nasdf get po -lpod-template-hash -oname
}; exit_stack_push unset -f app_replicaset_pod_name

app_list() {
	kubectl config use-context $1
	app_list_custom $1 ''
	echo
	app_list_custom $1 --sort-by=.metadata.creationTimestamp\
\	-ocustom-columns=Kind:.kind,Namespace:.metadata.namespace,Name:.metadata.name,CreationTime:.metadata.creationTimestamp\

}; exit_stack_push unset -f app_list

app_list_custom() {
	kubectl get\
		namespace/asdf\
		recipe/asdf\
		vrg/bb\
		$(pv_names $1)\
		-nasdf\
		vr/busybox-pvc\
		pvc/busybox-pvc\
		deploy/busybox\
		$(app_replicaset_pod_name $1)\
		$(app_deployment_replicaset_name $1)\
		po/asdf\
		cm/asdf\
		secret/asdf\
		$2
}; exit_stack_push unset -f app_list_custom

app_undeploy() {
	set -x
	kubectl --context $1 -nasdf delete --ignore-not-found -k https://github.com/RamenDR/ocm-ramen-samples/busybox
	kubectl --context $1 -nasdf delete --ignore-not-found po/asdf secret/asdf configmap/asdf recipe/asdf
	{ set +x;} 2>/dev/null
	app_namespace_undeploy $1
	app_list $1
}; exit_stack_push unset -f app_undeploy

vrg_apply() {
	vrg_appendix="
  kubeObjectProtection:
    captureInterval: 1m
    recipeRef:
      name: asdf$3${4:+
  action: $4}" \
	cluster_names=$s3_store_cluster_names application_sample_namespace_name=asdf $ramen_hack_directory_path_name/minikube-ramen.sh application_sample_vrg_deploy$2 $1
}; exit_stack_push unset -f vrg_apply

vrg_deploy() {
	vrg_apply $1 "$2" "$3" $4
	vrg_list $1
}; exit_stack_push unset -f vrg_deploy

vrg_deploy_failover() {
	vrg_deploy $1 "$2" "$3" Failover
}; exit_stack_push unset -f vrg_deploy_failover

vrg_deploy_relocate() {
	vrg_deploy $1 "$2" "$3" Relocate
}; exit_stack_push unset -f vrg_deploy_relocate

vrg_undeploy() {
	cluster_names=$s3_store_cluster_names application_sample_namespace_name=asdf $ramen_hack_directory_path_name/minikube-ramen.sh application_sample_vrg_undeploy $1
}; exit_stack_push unset -f vrg_undeploy

vrg_demote() {
	vrg_deploy_$2 $1 _sec
#	time kubectl --context $1 -nasdf wait vrg/bb --for condition=clusterdataprotected=false
}; exit_stack_push unset -f vrg_demote

vrg_final_sync() {
	vrg_apply $1 '' '
  prepareForFinalSync: true'
	until_true_or_n 30 eval test \"\$\(kubectl --context $1 -nasdf get vrg/bb -ojsonpath='{.status.prepareForFinalSyncComplete}'\)\" = true
	vrg_apply $1 '' '
  runFinalSync: true'
	until_true_or_n 30 eval test \"\$\(kubectl --context $1 -nasdf get vrg/bb -ojsonpath='{.status.finalSyncComplete}'\)\" = true
}; exit_stack_push unset -f vrg_final_sync

vrg_fence() {
	vrg_demote $1 failover
}; exit_stack_push unset -f vrg_fence

vrg_finalizer0_remove() {
	true_if_exit_status_and_stderr 1 'Error from server (NotFound): volumereplicationgroups.ramendr.openshift.io "bb" not found' \
	kubectl --context $1 -nasdf patch vrg/bb --type json -p '[{"op":remove, "path":/metadata/finalizers/0}]'
}; exit_stack_push unset -f vrg_finalizer0_remove

vr_finalizer0_remove() {
	true_if_exit_status_and_stderr 1 'Error from server (NotFound): volumereplications.replication.storage.openshift.io "busybox-pvc" not found' \
	kubectl --context $1 -nasdf patch volumereplication/busybox-pvc --type json -p '[{"op":remove, "path":/metadata/finalizers/0}]'
}; exit_stack_push unset -f vr_finalizer0_remove

vrg_get() {
	kubectl --context $1 -nasdf get vrg/bb --ignore-not-found -oyaml
}; exit_stack_push unset -f vrg_get

vrg_spec_get() {
	kubectl --context $1 -nasdf get vrg/bb -ojsonpath='{.spec}'|json_to_yaml
}; exit_stack_push unset -f vrg_spec_get

vrg_list() {
	set -x
	kubectl --context $1 -nasdf get vrg/bb --ignore-not-found
	{ set +x;} 2>/dev/null
}; exit_stack_push unset -f vrg_list

vrg_get_s3() {
	mc cp -q $(app_s3_object_name_prefix $1)v1alpha1.VolumeReplicationGroup/a /tmp/a.json.gz;gzip -df /tmp/a.json.gz;json_to_yaml </tmp/a.json
}; exit_stack_push unset -f vrg_get_s3

vr_get() {
	kubectl --context $1 -nasdf get volumereplication/busybox-pvc --ignore-not-found -oyaml
}; exit_stack_push unset -f vr_get

vr_list() {
	kubectl --context $1 -nasdf get volumereplication/busybox-pvc --ignore-not-found
}; exit_stack_push unset -f vr_list

vr_delete() {
	kubectl --context $1 -nasdf delete volumereplication/busybox-pvc --ignore-not-found
}; exit_stack_push unset -f vr_delete

pvc_get() {
	kubectl --context $1 -nasdf get pvc/busybox-pvc -oyaml --ignore-not-found
}; exit_stack_push unset -f pvc_get

pv_names() {
	kubectl --context $1 get pv -ojsonpath='{range .items[?(@.spec.claimRef.namespace=="asdf")]} pv/{.metadata.name}{end}'
}; exit_stack_push unset -f pv_names

pv_list() {
	kubectl --context $1 get $(pv_names $1) --show-kind
}; exit_stack_push unset -f pv_list

pv_get() {
	kubectl --context $1 get $(pv_names $1) -oyaml
}; exit_stack_push unset -f pv_get

pv_delete() {
	kubectl --context $1 delete $(pv_names $1)
}; exit_stack_push unset -f pv_delete

pv_unretain() {
	kubectl --context $1 patch $(pv_names $1) --type json -p '[{"op":add, "path":/spec/persistentVolumeReclaimPolicy, "value":Delete}]'
}; exit_stack_push unset -f pv_unretain

app_protect() {
	set -- cluster1
	vrg_deploy $1
	set -x
	time kubectl --context $1 -nasdf wait vrg/bb --for condition=clusterdataprotected --timeout -1s
	{ set +x; } 2>/dev/null
#	app_protection_info 1
}; exit_stack_push unset -f app_protect

app_unprotect() {
	vrg_undeploy $1
	kubectl --context $1 -nasdf delete events --all
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
	vrg_final_sync $2
	set -x
	time kubectl --context $2 -nasdf wait vrg/bb --for condition=clusterdataprotected --timeout -1s
	{ set +x; } 2>/dev/null
	date
	app_undeploy_failback $2 relocate app_recover_failback\ $1\ $2
}; exit_stack_push unset -f app_failback

app_recover() {
	app_namespace_deploy $1
	date
	app_recipe_deploy $1 # TODO remove once recipe protected
	vrg_deploy_$2 $1
	set -x
	time kubectl --context $1 -nasdf wait vrg/bb --for condition=clusterdataready --timeout -1s
	{ set +x; } 2>/dev/null
	app_list $1
	date
	set -x
	time kubectl --context $1 -nasdf wait vrg/bb --for condition=clusterdataprotected
	time kubectl --context $1 -nasdf wait vrg/bb --for condition=dataready --timeout 2m
	{ set +x; } 2>/dev/null
	until_true_or_n 30000 eval test \"\$\(kubectl --context $1 -nasdf get vrg/bb -ojsonpath='{.status.state}'\)\" = Primary
}; exit_stack_push unset -f app_recover

app_undeploy_failback() {
	vrg_demote $1 $2
	# "PVC not being deleted. Not ready to become Secondary"
	set -x
	time kubectl --context $1 -nasdf wait vrg/bb --for condition=clusterdataprotected --timeout -1s
	{ set +x; } 2>/dev/null
	app_undeploy $1& # pvc finalizer remains until vrg deletes its vr
	until_true_or_n 30000 eval test \"\$\(kubectl --context $1 -nasdf get vrg/bb -ojsonpath='{.status.state}'\)\" = Secondary
	$3
	vrg_undeploy $1&
	date
	time wait
	date
}; exit_stack_push unset -f app_undeploy_failback

app_recover_failback() {
	# "VolumeReplication resource for the pvc as Secondary is in sync with Primary"
	set -x
	time kubectl --context $2 -nasdf wait vrg/bb --for condition=dataprotected --timeout 10m
	{ set +x; } 2>/dev/null
	app_recover $1 relocate
}; exit_stack_push unset -f app_recover_failback

app_velero_kube_object_name=asdf--bb--
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
	echo $1/bucket/asdf/bb/
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
	velero --kubecontext $1 get backups
	velero --kubecontext $1 get backup-locations
	velero --kubecontext $1 get restores
}; exit_stack_push unset -f velero_kube_objects_list

velero_kube_objects_undeploy() {
	velero_kube_objects_list $1
	velero --kubecontext $1 delete --all --confirm backups
	velero --kubecontext $1 delete --all --confirm backup-locations
	velero --kubecontext $1 delete --all --confirm restores
	velero_kube_objects_list $1
}; exit_stack_push unset -f velero_kube_objects_undeploy

velero_kube_objects_delete() {
	kubectl --context $1 -n velero delete --all restores,backups,backupstoragelocations
}; exit_stack_push unset -f velero_kube_objects_delete

s3_store_cluster_names=${s3_store_cluster_names-cluster2\ cluster1}
exit_stack_push unset -v s3_store_cluster_names

"$@"
