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
	mc alias set cluster2 $(minikube_minio_url cluster2) minio minio123
mc tree cluster2
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
kubectl --context $1        delete namespace asdf&
	vrg_finalizer0_remove $1
wait
}; exit_stack_push unset -f app_undeploy

vrg_deploy() {
	vrg_appendix='
  kubeObjectProtection: {}' \
	vrg_appendix='
  kubeObjectProtection:
    captureInterval: 1m' \
	cluster_names=cluster2 application_sample_namespace_name=asdf $ramen_hack_directory_path_name/minikube-ramen.sh  application_sample_vrg_deploy\ $1
}; exit_stack_push unset -f vrg_deploy

vrg_finalizer0_remove() {
	true_if_exit_status_and_stderr 1 'Error from server (NotFound): volumereplicationgroups.ramendr.openshift.io "bb" not found' \
	kubectl --context $1 -nasdf patch vrg/bb --type json -p '[{"op":remove, "path":/metadata/finalizers/0}]'
}; exit_stack_push unset -f vrg_finalizer0_remove

vrg_get() {
	kubectl --context $1 -nasdf get vrg/bb -oyaml --ignore-not-found
}; exit_stack_push unset -f vrg_get

app_protect() {
	vrg_deploy cluster1
	vrg_get cluster1
time kubectl --context cluster1 -nasdf wait vrg/bb --for condition=clusterdataprotected --timeout 2m
	app_protection_info 0
}; exit_stack_push unset -f app_protect

app_unprotect() {
	cluster_names=cluster2 application_sample_namespace_name=asdf $ramen_hack_directory_path_name/minikube-ramen.sh application_sample_vrg_undeploy\ $1&
	vrg_finalizer0_remove $1
	wait
	kubectl --context $1 -nasdf delete events --all
}; exit_stack_push unset -f app_unprotect

app_recover() {
	kubectl create namespace asdf --dry-run=client -oyaml|kubectl --context cluster2 apply -f-
	vrg_deploy cluster2
time kubectl --context cluster2 -nasdf wait vrg/bb --for condition=clusterdataready --timeout 2m
	app_kube_objects_list cluster2
}; exit_stack_push unset -f app_recover

s3_objects_list() {
mc tree cluster2
mc ls cluster2 --recursive
}; exit_stack_push unset -f s3_objects_list

app_s3_object_name_prefix() {
	echo cluster2/bucket/asdf/bb/
}; exit_stack_push unset -f app_s3_object_name_prefix

app_s3_object_name_prefix_velero() {
	echo $(app_s3_object_name_prefix)velero/
}; exit_stack_push unset -f app_s3_object_name_prefix_velero

app_s3_objects_delete() {
mc rm $(app_s3_object_name_prefix) --recursive --force
}; exit_stack_push unset -f app_objects_delete

app_protection_info() {
mc cp -q $(app_s3_object_name_prefix_velero)backups/$1/$1-resource-list.json.gz /tmp;gzip -df /tmp/$1-resource-list.json.gz;python -m json.tool </tmp/$1-resource-list.json
mc cp -q $(app_s3_object_name_prefix_velero)backups/$1/$1-logs.gz /tmp;gzip -df /tmp/$1-logs.gz;cat /tmp/$1-logs
}; exit_stack_push unset -f app_protection_info

app_recovery_info() {
mc cp -q $(app_s3_object_name_prefix_velero)restores/$1/restore-$1-results.gz /tmp;gzip -df /tmp/restore-$1-results.gz;python -m json.tool </tmp/restore-$1-results
mc cp -q $(app_s3_object_name_prefix_velero)restores/$1/restore-$1-logs.gz /tmp;gzip -df /tmp/restore-$1-logs.gz;cat /tmp/restore-$1-logs
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

exit_stack_push unset -v command
set -x
for command in "${@:-infra_deploy app_deploy app_protect}"; do
	$command
done
{ set +x;} 2>/dev/null
