#!/bin/bash
# Recipe e2e failover script

set -e

VRG_NAME=bb 
BUCKET_NAME=velero
NAMESPACE=recipe-test
CLUSTER_FROM=cluster1
CLUSTER_TO=cluster2
MINIO_PROFILE=minio-cluster1
DEPLOYMENT_NAME=busybox
WORKING_DIRECTORY=$(pwd)

# shellcheck source=hack/recipe_e2e/scripts/recipe_e2e_functions.sh
source "scripts/recipe_e2e_functions.sh"
export -f is_restore_hook_successful
export -f get_restore_index
export -f wait_for_vrg_state
export -f wait_for_pv_unbound
export -f wait_for_resource_deletion
export -f wait_for_resource_creation

set -x

# ensure cluster is ready for failover
if [[ $(kubectl get vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_FROM --no-headers | wc -l) -eq 0 ]]; then
    echo "VRG $VRG_NAME not found in '$NAMESPACE' namespace. Exiting."
    exit 1
fi

if [[ $(kubectl get vrg/$VRG_NAME -n $NAMESPACE -o yaml --context $CLUSTER_FROM | grep state: | awk '{print $2}') != "Primary" ]]; then
    echo "VRG $VRG should be in Primary state to begin Failover. Exiting."
    exit 1
fi

# ensure VRG is in Primary state
wait_for_vrg_state "Primary" vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_FROM

# create fence on cluster1: change cluster1 vrg from Primary to Secondary
KUBE_EDITOR="sed -i 's/replicationState: primary/replicationState: secondary/g'" kubectl edit vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_FROM
kubectl patch vrg/$VRG_NAME --type json -p '[{"op": add, "path":/spec/action, "value": Failover}]' -n $NAMESPACE --context $CLUSTER_FROM

# undeploy cluster1 application
kubectl delete deployment/$DEPLOYMENT_NAME -n $NAMESPACE --context $CLUSTER_FROM

# wait for application to be deleted
wait_for_resource_deletion all -n $NAMESPACE --context $CLUSTER_FROM

# cluster1 VRG should be Secondary
wait_for_vrg_state "Secondary" vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_FROM

# switch to cluster2
kubectl config use-context $CLUSTER_TO

# change to Ramen directory
cd ../.. 

# deploy Ramen on cluster2
make deploy-dr-cluster

# wait for ramen-system to run
kubectl wait deployment.apps/ramen-dr-cluster-operator -n ramen-system --context $CLUSTER_TO --for condition=available --timeout 60s

# optionally create namespace on Failover cluster
if [[ $(kubectl get namespace $NAMESPACE --no-headers --context $CLUSTER_TO | wc -l) -eq 0 ]]; then
    kubectl create namespace $NAMESPACE --context $CLUSTER_TO
fi

# configure Ramen; this creates Primary VRG
cd "$WORKING_DIRECTORY"
bash scripts/deploy_primary.sh

# wait for deployment to exist
wait_for_resource_creation deployment/$DEPLOYMENT_NAME -n $NAMESPACE --context $CLUSTER_TO

# wait for new application to come online 
kubectl wait deployment/$DEPLOYMENT_NAME -n $NAMESPACE --for condition=available --timeout=60s --context=$CLUSTER_TO

# application is ready. Verify restore contents and backup hook (order of Workflow matters)
RESTORE_RESULTS_FILE=restore-$NAMESPACE--$VRG_NAME--$RESTORE_NUMBER-logs.gz
RESTORE_INDEX=$(get_restore_index $MINIO_PROFILE/$BUCKET_NAME/$NAMESPACE/$VRG_NAME/kube-objects)

# verify hook: should run as Backup, not Restore
is_restore_hook_successful $MINIO_PROFILE/$BUCKET_NAME/$NAMESPACE/$VRG_NAME/kube-objects/0/$BUCKET_NAME/backups/$NAMESPACE--$VRG_NAME--0--use-backup-not-restore-restore-0--$MINIO_PROFILE/velero-backup.json

RESTORE_NUMBER=1
RESTORE_RESULTS_FILE=restore-$NAMESPACE--$VRG_NAME--$RESTORE_NUMBER-logs.gz
RESTORE_GROUP_1=$MINIO_PROFILE/$BUCKET_NAME/$NAMESPACE/$VRG_NAME/kube-objects/$RESTORE_INDEX/velero/restores/recipe-test--bb--$RESTORE_NUMBER/$RESTORE_RESULTS_FILE
verify_restore_success "$RESTORE_GROUP_1"

RESTORE_NUMBER=2
RESTORE_RESULTS_FILE=restore-$NAMESPACE--$VRG_NAME--$RESTORE_NUMBER-logs.gz
RESTORE_GROUP_2=$MINIO_PROFILE/$BUCKET_NAME/$NAMESPACE/$VRG_NAME/kube-objects/$RESTORE_INDEX/velero/restores/recipe-test--bb--$RESTORE_NUMBER/$RESTORE_RESULTS_FILE
verify_restore_success "$RESTORE_GROUP_2"

 # cluster2 vrg should transition to Primary
wait_for_vrg_state "Primary" vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_TO

# remove Failover action from old Primary
kubectl patch vrg/$VRG_NAME --type json -p '[{"op": remove, "path":/spec/action}]' -n $NAMESPACE --context $CLUSTER_FROM

# show S3 contents
mc du -r $MINIO_PROFILE

# wait for conditions DataReady, ClusterDataReady on cluster2
echo "failover successful"

