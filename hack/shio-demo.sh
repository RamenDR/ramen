#!/bin/sh
# shellcheck disable=1090,2046,2086
set -e

ramen_hack_directory_path_name=$(dirname $0)
. $ramen_hack_directory_path_name/exit_stack.sh
exit_stack_push unset -v ramen_hack_directory_path_name
. $ramen_hack_directory_path_name/minikube.sh; exit_stack_push minikube_unset
. $ramen_hack_directory_path_name/true_if_exit_status_and_stderr.sh; exit_stack_push unset -f true_if_exit_status_and_stderr

infra_deploy() {
$ramen_hack_directory_path_name/minikube-ramen.sh deploy
minikube profile list
$ramen_hack_directory_path_name/velero-test.sh velero_deploy\ cluster1
$ramen_hack_directory_path_name/velero-test.sh velero_deploy\ cluster2
velero_secret_deploy cluster1
velero_secret_deploy cluster2
kubectl --context cluster1 -nvelero get bsl,backup,restore
kubectl --context cluster2 -nvelero get bsl,backup,restore
	for cluster_name in $s3_store_cluster_names; do
		mc alias set $cluster_name $(minikube_minio_url $cluster_name) minio minio123
		mc tree $cluster_name
	done; unset -v cluster_name
}; exit_stack_push unset -f infra_deploy

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

app_deploy() {
kubectl --context cluster1        create namespace asdf
kubectl --context cluster1 -nasdf create configmap asdf
kubectl --context cluster1 -nasdf create secret generic asdf --from-literal=key1=value1
kubectl --context cluster1 -nasdf create deploy asdf --image busybox -- sh -c while\ true\;do\ date\;sleep\ 60\;done
kubectl --context cluster1 -nasdf create -k https://github.com/RamenDR/ocm-ramen-samples/busybox
	app_kube_objects_list cluster1
}; exit_stack_push unset -f app_deploy

app_kube_objects_list() {
kubectl --context $1 -nasdf get cm,secret,deploy,rs,po,pvc
}; exit_stack_push unset -f app_kube_objects_list

app_undeploy() {
kubectl --context $1        delete namespace asdf
}; exit_stack_push unset -f app_undeploy

vrg_deploy() {
	vrg_appendix='
  kubeObjectProtection: {}' \
	vrg_appendix='
  kubeObjectProtection:
    captureInterval: 1m
    recoverOrder:
      - excludedResources:
          - po
          - rs
          - deploy
      - includedResources:
          - deployments
' \
	cluster_names=$s3_store_cluster_names application_sample_namespace_name=asdf $ramen_hack_directory_path_name/minikube-ramen.sh application_sample_vrg_deploy$2\ $1
}; exit_stack_push unset -f vrg_deploy

vrg_demote() {
	vrg_deploy $1 _sec
}; exit_stack_push unset -f vrg_demote

vrg_finalizer0_remove() {
	true_if_exit_status_and_stderr 1 'Error from server (NotFound): volumereplicationgroups.ramendr.openshift.io "bb" not found' \
	kubectl --context $1 -nasdf patch vrg/bb --type json -p '[{"op":remove, "path":/metadata/finalizers/0}]'
}; exit_stack_push unset -f vrg_finalizer0_remove

vrg_get() {
	kubectl --context $1 -nasdf get vrg/bb -oyaml --ignore-not-found
}; exit_stack_push unset -f vrg_get

vrg_get_s3() {
	mc cp -q $(app_s3_object_name_prefix $1)v1alpha1.VolumeReplicationGroup/a /tmp/a.json.gz;gzip -df /tmp/a.json.gz;python -m json.tool </tmp/a.json
}; exit_stack_push unset -f vrg_get_s3

app_protect() {
	vrg_deploy cluster1
	vrg_get cluster1
time kubectl --context cluster1 -nasdf wait vrg/bb --for condition=clusterdataprotected --timeout 2m
	app_protection_info 1
}; exit_stack_push unset -f app_protect

app_unprotect() {
	cluster_names=$s3_store_cluster_names application_sample_namespace_name=asdf $ramen_hack_directory_path_name/minikube-ramen.sh application_sample_vrg_undeploy\ $1
	kubectl --context $1 -nasdf delete events --all
	velero_kube_objects_list $1
	s3_objects_list
}; exit_stack_push unset -f app_unprotect

app_recover() {
	vrg_demote cluster1
	kubectl create namespace asdf --dry-run=client -oyaml|kubectl --context cluster2 apply -f-
	vrg_deploy cluster2
time kubectl --context cluster2 -nasdf wait vrg/bb --for condition=clusterdataready --timeout 2m
	app_kube_objects_list cluster2
}; exit_stack_push unset -f app_recover

app_velero_kube_object_name() {
	echo asdf--bb--$1----minio-on-$2
}; exit_stack_push unset -f app_velero_kube_object_name

s3_objects_list() {
	for cluster_name in $s3_store_cluster_names; do
		mc tree $cluster_name
		mc ls $cluster_name --recursive
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
		mc rm $(app_s3_object_name_prefix $cluster_name) --recursive --force
	done; unset -v cluster_name
}; exit_stack_push unset -f app_objects_delete

app_protection_info() {
	for cluster_name in $s3_store_cluster_names; do
		set -- "$1" $(app_velero_kube_object_name "$1" $cluster_name) $(app_s3_object_name_prefix_velero "$1" $cluster_name)
		mc cp -q $3backups/$2/velero-backup.json /tmp/$2-velero-backup.json;python -m json.tool </tmp/$2-velero-backup.json
		mc cp -q $3backups/$2/$2-resource-list.json.gz /tmp;gzip -df /tmp/$2-resource-list.json.gz;python -m json.tool </tmp/$2-resource-list.json
		mc cp -q $3backups/$2/$2-logs.gz /tmp;gzip -df /tmp/$2-logs.gz;cat /tmp/$2-logs
	done; unset -v cluster_name
}; exit_stack_push unset -f app_protection_info

app_recovery_info() {
	for cluster_name in $s3_store_cluster_names; do
		set -- "$1" $(app_velero_kube_object_name "$1" $cluster_name) $(app_s3_object_name_prefix_velero "$1" $cluster_name)
		mc cp -q $3restores/$2/restore-$2-results.gz /tmp;gzip -df /tmp/restore-$2-results.gz;python -m json.tool </tmp/restore-$2-results
		mc cp -q $3restores/$2/restore-$2-logs.gz /tmp;gzip -df /tmp/restore-$2-logs.gz;cat /tmp/restore-$2-logs
	done; unset -v cluster_name
}; exit_stack_push unset -f app_recovery_info

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
exit_stack_push unset -v command
set -x
for command in "${@:-infra_deploy app_deploy app_protect}"; do
	$command
done
{ set +x;} 2>/dev/null
