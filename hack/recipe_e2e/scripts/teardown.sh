#!/bin/bash
set -x

CLUSTER=${1:-cluster1}
MINIO_ALIAS=${2:-minio-cluster1}
NAMESPACE=${3:-recipe-test}
#BUCKET=${4:-velero}
VRG_NAME=${5:-bb}
VR_NAME=busybox-pvc
PVC_NAME=busybox-pvc
DEPLOYMENT_NAME=busybox

BASE_RAMEN_DIRECTORY=../..
WORKING_DIRECTORY=$(pwd)

# shellcheck source=hack/recipe_e2e/scripts/recipe_e2e_functions.sh
source "scripts/recipe_e2e_functions.sh"

echo "tearing down cluster Ramen and namespace '$NAMESPACE' in '$CLUSTER' and Minio alias '$MINIO_ALIAS'"

# delete application
if [[ $(kubectl get all -n "$NAMESPACE" --context "$CLUSTER" | wc -l) -gt 0 ]]; then 
    echo "undeploying application"
    kubectl delete deployment/"$DEPLOYMENT_NAME" -n "$NAMESPACE" --context "$CLUSTER"
fi

# VRG deletion sequence part 1/3: delete VR
if [[ $(kubectl get vr/"$VR_NAME" -n "$NAMESPACE" --context "$CLUSTER" --no-headers | wc -l ) -gt 0 ]]; then
    kubectl delete vr/"$VR_NAME" -n "$NAMESPACE" --context "$CLUSTER"
fi

# VRG deletion sequence part 2/3: delete VRG
if [[ $(kubectl get vrg/"$VRG_NAME" -n "$NAMESPACE" --context "$CLUSTER" --no-headers | wc -l) -gt 0 ]]; then
    kubectl delete vrg/"$VRG_NAME" -n "$NAMESPACE" --context "$CLUSTER"
fi

# VRG deletion sequence part 3/3: delete PVC
if [[ $(kubectl get pvc/"$PVC_NAME" -n "$NAMESPACE" --context "$CLUSTER" | wc -l) -gt 0 ]]; then
    # delete PVC
    kubectl delete pvc/"$PVC_NAME" -n "$NAMESPACE" --context "$CLUSTER"
fi

# delete ramen
if [[ $(kubectl get all -n ramen-system --context "$CLUSTER" | wc -l) -gt 0 ]]; then 
    echo "undeploying ramen-system"
    kubectl config use-context "$CLUSTER"
    cd "$BASE_RAMEN_DIRECTORY" || exit
    make undeploy-dr-cluster
    cd "$WORKING_DIRECTORY" || exit
fi

# cleanup s3 store
 bash scripts/cleanup_s3.sh "$MINIO_ALIAS" "$NAMESPACE"

# cleanup PVs if Available or Released
for pv in $(kubectl get pv --context "$CLUSTER" | grep "$PVC_NAME" | awk '{print $1}');
do 
    STATUS=$(kubectl get pv/"$pv" --context "$CLUSTER" --no-headers | awk '{print $5}')
    echo "pv/$pv status: $STATUS. "

    if [[ "$STATUS" == "Available" || "$STATUS" == "Released" ]]; then
        kubectl delete pv/"$pv" --context "$CLUSTER"
    else
        printf "Skipping.\n"
    fi
done

echo "'$CLUSTER' cleanup complete"
