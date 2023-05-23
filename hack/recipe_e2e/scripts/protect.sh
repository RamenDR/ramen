#!/bin/bash
CLUSTER=cluster1
MINIO_PROFILE=minio-cluster1
BUCKET_NAME=velero
NAMESPACE=recipe-test
VRG_NAME=bb
BACKUP_GROUP_NAME=instance-resources
BACKUP_HOOK_NAME=service-hooks-pre-backup
BACKUP_START_INDEX=1
WORKING_DIRECTORY=$(pwd)

set -x
set -e

# shellcheck source=hack/recipe_e2e/scripts/recipe_e2e_functions.sh
source "scripts/recipe_e2e_functions.sh"
export -f wait_for_and_check_backup_success
export -f wait_for_vrg_state

# set context to cluster1
kubectl config use-context $CLUSTER

# change to base ramen directory
cd ../..

# deploy ramen to cluster1
make deploy-dr-cluster

# wait for ramen deployment to become available
kubectl wait deployment.apps/ramen-dr-cluster-operator -n ramen-system --context $CLUSTER --for condition=available --timeout=60s

# optionally create namespace
if [[ $(kubectl get namespace $NAMESPACE --no-headers --context $CLUSTER | wc -l) -eq 0 ]]; then
    kubectl create namespace $NAMESPACE --context $CLUSTER
fi

# cluster1: setup app
application_sample_namespace_name=$NAMESPACE bash hack/minikube-ramen.sh application_sample_deploy $CLUSTER

cd "$WORKING_DIRECTORY"

# wait for deployment to become ready
kubectl wait deployments.apps/busybox -n $NAMESPACE --for condition=available --timeout=60s --context=$CLUSTER

# setup s3
kubectl apply -f config/ramen_secret_minio.yaml 
kubectl apply -f config/ramen_config.yaml

# create VRG
kubectl apply -f protect/vrg_busybox_primary.yaml
kubectl apply -f protect/recipe_busybox.yaml

# wait for VRG to become Primary
wait_for_vrg_state "Primary" vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER

# check s3 for backups
mc du -r $MINIO_PROFILE

# wait for backups to be created according to Recipe spec
# backups should be successful (totalItems > 0); 5m replication period by default, starts at index 1
BACKUP_HOOK=$MINIO_PROFILE/$BUCKET_NAME/$NAMESPACE/$VRG_NAME/kube-objects/$BACKUP_START_INDEX/velero/backups/$NAMESPACE--$VRG_NAME--$BACKUP_HOOK_NAME--$MINIO_PROFILE/velero-backup.json
wait_for_and_check_backup_success $BACKUP_HOOK

BACKUP_RESOURCES=$MINIO_PROFILE/$BUCKET_NAME/$NAMESPACE/$VRG_NAME/kube-objects/$BACKUP_START_INDEX/velero/backups/$NAMESPACE--$VRG_NAME--$BACKUP_GROUP_NAME--$MINIO_PROFILE/velero-backup.json
wait_for_and_check_backup_success $BACKUP_RESOURCES

echo "protection successful"
exit 0
