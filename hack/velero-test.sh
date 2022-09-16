#!/bin/sh
# shellcheck disable=1090,2046,2086
set -e
ramen_hack_directory_path_name=$(dirname $0)
. $ramen_hack_directory_path_name/exit_stack.sh
exit_stack_push unset -v ramen_hack_directory_path_name
. $ramen_hack_directory_path_name/minikube.sh
exit_stack_push minikube_unset
velero_directory_path_name=~/.local/bin

. $ramen_hack_directory_path_name/until_true_or_n.sh

velero_crds_kubectl()
{
	kubectl --context $1 $2\
		-f https://raw.githubusercontent.com/vmware-tanzu/velero/main/config/crd/v1/bases/velero.io_backupstoragelocations.yaml\
		-f https://raw.githubusercontent.com/vmware-tanzu/velero/main/config/crd/v1/bases/velero.io_backups.yaml\
		-f https://raw.githubusercontent.com/vmware-tanzu/velero/main/config/crd/v1/bases/velero.io_restores.yaml\

}
velero_deploy()
{
	set -- $1 apply
	velero_crds_kubectl $1 $2
	# https://github.com/vmware-tanzu/velero/blob/main/pkg/install/resources.go
	# https://github.com/vmware-tanzu/velero/blob/main/pkg/install/deployment.go
	cat <<-a|kubectl --context $1 $2 -f -
	---
	apiVersion: v1
	kind: Namespace
	metadata:
	  name: velero
	---
	apiVersion: rbac.authorization.k8s.io/v1
	kind: ClusterRoleBinding
	metadata:
	  name: velero
	subjects:
	  - kind: ServiceAccount
	    namespace: velero
	    name: velero
	roleRef:
	  - apiGroup: rbac.authorization.k8s.io
	    kind: ClusterRole
	    name: cluster-admin
	a
}
velero_undeploy()
{
	set -- $1 delete
	velero_crds_kubectl $1 $2
}
velero_deploy()
{
	set -- $1 $velero_directory_path_name
	$ramen_hack_directory_path_name/velero-install.sh $2
	$2/velero --kubecontext $1 install\
		--no-secret\
		--no-default-backup-location\
		--use-volume-snapshots=false\
		--plugins velero/velero-plugin-for-aws:v1.4.0\

}
velero_undeploy()
{
	date
	kubectl --context $1 delete namespace/velero clusterrolebinding/velero
	date
	kubectl --context $1 delete crds -l component=velero
}
velero_undeploy()
{
	./velero --kubecontext $1 uninstall --force
}
minio_deploy()
{
	$ramen_hack_directory_path_name/ocm-minikube-ramen.sh rook_ceph_deploy_spoke\ $1 minio_deploy\ $1
}
velero_backup()
{
	# TODO get s3 configuration from ramen config map and secret
	cat <<-a|kubectl --context $1 -n velero apply -f -
	---
	apiVersion: v1
	kind: Secret
	metadata:
	  name: s3secret
	stringData:
	  aws: |
	    [default]
	    aws_access_key_id=$3
	    aws_secret_access_key=$4
	---
	apiVersion: velero.io/v1
	kind: BackupStorageLocation
	metadata:
	  name: l
	spec:
	  provider: aws
	  objectStorage:
	    bucket: $5
	  config:
	    region: us-east-1
	    s3ForcePathStyle: "true"
	    s3Url: $2
	  credential:
	    name: s3secret
	    key: aws
	---
	apiVersion: velero.io/v1
	kind: Backup
	metadata:
	  name: b
	spec:
	  storageLocation: l$7
	a
        #until_true_or_n 30 eval test \"\$\(kubectl --context $1 -n velero get backups/b -ojsonpath='{.status.phase}'\)\" = Completed
	minio_bucket_list $6 $2 $3 $4 $5
}
velero_backup_namespace()
{
	velero_backup $1 $3 $4 $5 $6 $7 "
  includedNamespaces:
  - $2
  includedResources:
  - po
  labelSelector:
    matchExpressions:
    - key: pod-template-hash
      operator: Exists"
}
velero_backup_dummy()
{
	velero_backup $1 $2 $3 $4 $5 $6 "
  includedNamespaces:
    - velero
  includedResources:
    - secrets
  labelSelector:
    matchLabels:
      dummyKey: dummyValue"
}
velero_restore_backup()
{
	velero_backup_dummy $1 $2 $3 $4 $5 $5 $7
	velero_restore_create $1 b b
}
velero_restore_create()
{
	cat <<-a|kubectl --context $1 -n velero apply -f -
	---
	apiVersion: velero.io/v1
	kind: Restore
	metadata:
	  name: $2
	spec:
	  backupName: $3
	a
#	until_true_or_n 30 eval test \"\$\(kubectl --context $1 -n velero get restores/$2 -ojsonpath='{.status.phase}'\)\" = Completed
}
velero_backup_delete()
{
	cat <<-a|kubectl --context $1 -n velero apply -f -
	---
	apiVersion: velero.io/v1
	kind: DeleteBackupRequest
	metadata:
	  name: $2
	spec:
	  backupName: $3
	a
#	until_true_or_n 30 eval test \"\$\(kubectl --context $1 -n velero get restores/$2 -ojsonpath='{.status.phase}'\)\" = Completed
}
minio_bucket_list()
{
	# TODO install mc
	mc alias set $1 $2 $3 $4
	mc ls $1/$5/backups/b/
	mc tree $1/$5
}
s3_username=minio
s3_password=minio123
s3_bucket_name=bucket
velero_backup_test()
{
	objects_deploy $1 $3
#	velero_deploy $1
#	minio_deploy $2
	velero_backup_namespace $1 $3 $(minikube_minio_url $2) $s3_username $s3_password $s3_bucket_name $2
#	velero_undeploy $1
}
velero_restore_test()
{
#	velero_deploy $1
	velero_restore_backup $1 $(minikube_minio_url $1) $s3_username $s3_password $s3_bucket_name $1
#	velero_undeploy $1
}
velero_test()
{
	set -- cluster2 cluster2 default
	set -- cluster1 cluster2 default
	velero_backup_test $1 $2 $3
	velero_restore_test $2
}
velero_objects_get()
{
	velero --kubecontext $1 get backup-locations
	velero --kubecontext $1 get backups
	velero --kubecontext $1 get restores
}
velero_objects_delete()
{
	velero --kubecontext $1 delete --all --confirm restores
	velero --kubecontext $1 delete --all --confirm backups
	velero --kubecontext $1 delete --all --confirm backup-locations
}
namespace_deploy()
{
	kubectl create --dry-run=client -oyaml namespace $2|kubectl --context $1 apply -f-
}
namespace_objects_get()
{
	kubectl --context $1 -n$2 get vrg,configmaps,secrets,deployments,replicasets,pods,pvc,pv
}
get()
{
	velero_objects_get $1
	namespace_objects_get $1 $2
}
objects_kubectl()
{
#	$(kubectl create --dry-run=client -oyaml -n$2 configmap asdf)
#	---
#	$(kubectl create --dry-run=client -oyaml -n$2 secret generic asdf --from-literal=asdf1=asdf2)
#	---
	cat <<-a|kubectl --context $1 $3 -f-
	---
	$(kubectl create --dry-run=client -oyaml namespace $2)
	---
	$(kubectl create --dry-run=client -oyaml -n$2 deploy asdf --image busybox -- sh -c while\ true\;do\ date\;sleep\ 60\;done)
	---
	a
}
objects_deploy()
{
	objects_kubectl $1 $2 apply
}
objects_undeploy()
{
	objects_kubectl $1 $2 delete\ --ignore-not-found
}
vrg_kubectl()
{
	cat <<-a|kubectl --context $1 $3 -f-
	---
	apiVersion: ramendr.openshift.io/v1alpha1
	kind: VolumeReplicationGroup
	metadata:
	  name: bb
	  namespace: $2
	spec:
	  async:
	    replicationClassSelector: {}
	    schedulingInterval: 1m
	  pvcSelector:
	    matchLabels:
	      appname: busybox
	  replicationState: primary
	  s3Profiles:
	  # - minio-on-cluster1
	  - minio-on-hub
	a
}
vrg_deploy()
{
	vrg_kubectl $1 $2 apply
}
vrg_undeploy()
{
	vrg_kubectl $1 $2 delete
}
failover1()
{
	set -- $cluster_names $namespace_name
	undeploy $2 $3
	vrg_deploy $1 $3
}
failover2()
{
	set -- $cluster_names $namespace_name
	undeploy $1 $3
	vrg_deploy $2 $3
}
od1() { set -- $cluster_names; objects_deploy $1 $namespace_name; }
od2() { set -- $cluster_names; objects_deploy $2 $namespace_name; }
ou1() { set -- $cluster_names; objects_undeploy $1 $namespace_name; }
ou2() { set -- $cluster_names; objects_undeploy $2 $namespace_name; }
d1()  { set -- $cluster_names; vrg_deploy $1 $namespace_name; }
d2()  { set -- $cluster_names; vrg_deploy $2 $namespace_name; }
u1()  { set -- $cluster_names; vrg_undeploy $1 $namespace_name; }
u2()  { set -- $cluster_names; vrg_undeploy $2 $namespace_name; }
g1()  { set -- $cluster_names; namespace_objects_get $1 $namespace_name; }
g2()  { set -- $cluster_names; namespace_objects_get $2 $namespace_name; }
vg1() { set -- $cluster_names; velero_objects_get $1; }
vg2() { set -- $cluster_names; velero_objects_get $2; }
vu1() { set -- $cluster_names; velero_objects_delete $1; }
vu2() { set -- $cluster_names; velero_objects_delete $2; }
cluster_names=cluster1\ hub
namespace_name=asdf
set -x
"${@:-velero_test}"
