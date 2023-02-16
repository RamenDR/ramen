#!/bin/bash
# Recipe e2e failback script

set -x
set -e

VRG_NAME=bb
VR_NAME=busybox-pvc
PVC_NAME=busybox-pvc
DEPLOYMENT_NAME=busybox
#BUCKET_NAME=velero
NAMESPACE=recipe-test
CLUSTER_TO=cluster1
CLUSTER_FROM=cluster2
#MINIO_PROFILE=minio-cluster1

INDEX_DATA_READY=0
#INDEX_DATA_PROTECTED=1
INDEX_CLUSTER_DATA_READY=2
INDEX_CLUSTER_DATA_PROTECTED=3

# shellcheck source=hack/recipe_e2e/scripts/recipe_e2e_functions.sh
source "scripts/recipe_e2e_functions.sh"
export -f is_restore_hook_successful
export -f get_restore_index
export -f wait_for_vrg_state

# scenario: cluster1 vrg=secondary, cluster2 vrg=primary

# verify original states: cluster1 VRG=Secondary, cluster2 VRG=Primary
if [[ $(kubectl get vrg/$VRG_NAME -n $NAMESPACE -o yaml --context $CLUSTER_FROM | grep state: | awk '{print $2}') != "Primary" ]]; then
    echo "$CLUSTER_FROM VRG $VRG should be in Primary state to begin Failback. Exiting."
    exit 1
fi

# 1) set cluster1 VRG to secondary (should already be done)
if [[ $(kubectl get vrg/$VRG_NAME -n $NAMESPACE -o yaml --context $CLUSTER_TO | grep state: | awk '{print $2}') != "Secondary" ]]; then
    echo "$CLUSTER_TO VRG $VRG should be in Secondary state to begin Failback. Exiting."
    exit 1
fi

# 2) undeploy application on cluster1 (should already be done)
wait_for_resource_deletion all -n $NAMESPACE --context $CLUSTER_TO

# 3) Delete the VRG on cluster1 here. Sequence: 1) VR, 2) VRG, 3) PVC (+PV)
# VRG deletion sequence part 1/3: delete VR
if [[ $(kubectl get vr/$VR_NAME -n $NAMESPACE --context $CLUSTER_TO --no-headers | wc -l ) -gt 0 ]]; then
    kubectl delete vr/$VR_NAME -n $NAMESPACE --context $CLUSTER_TO
fi

# VRG deletion sequence part 2/3: delete VRG
if [[ $(kubectl get vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_TO --no-headers | wc -l) -gt 0 ]]; then
    kubectl delete vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_TO
fi

# PVC RetainPolicy may change from Retain to Delete after VRG is deleted. Keep Retain policy.
PV_NAME=$(kubectl get pv --context $CLUSTER_TO | grep $NAMESPACE/$PVC_NAME| awk '{print $1}')
RECLAIM_POLICY=$(kubectl get pv/"$PV_NAME" --context $CLUSTER_TO -o=jsonpath="{.spec.persistentVolumeReclaimPolicy}")
if [[ "$RECLAIM_POLICY" != "Retain" ]]; then
    echo "changing PV reclaim policy from $RECLAIM_POLICY to Retain"
    KUBE_EDITOR="sed -i 's/persistentVolumeReclaimPolicy: $RECLAIM_POLICY/persistentVolumeReclaimPolicy: Retain/g'" kubectl edit pv/"$PV_NAME" --context $CLUSTER_TO
fi

# VRG deletion sequence part 3/3: delete PVC
if [[ $(kubectl get pvc/$PVC_NAME -n $NAMESPACE --context $CLUSTER_TO | wc -l) -gt 0 ]]; then
    # delete PVC
    kubectl delete pvc/$PVC_NAME -n $NAMESPACE --context $CLUSTER_TO
fi

# 4) unfence VRGs; not necessary

# 5) unfence from VRG kube objects volume data - nothing to be done here

# 6) wait for cluster2 VRG to have condition ClusterDataProtected=True
wait_for_vrg_condition_status $INDEX_CLUSTER_DATA_PROTECTED True vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_FROM

#  7) change cluster2 vrg from Primary to Secondary, notify VRG of Failback procedure
KUBE_EDITOR="sed -i 's/replicationState: primary/replicationState: secondary/g'" kubectl edit vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_FROM
kubectl patch vrg/$VRG_NAME --type json -p '[{"op": add, "path":/spec/action, "value": Relocate}]' -n $NAMESPACE --context $CLUSTER_FROM

# 8) undeploy cluster2 application and wait for it to be deleted
kubectl delete deployment/$DEPLOYMENT_NAME -n $NAMESPACE --context $CLUSTER_FROM

wait_for_resource_deletion all -n $NAMESPACE --context $CLUSTER_FROM

# 9a) setup cluster1 VRG again, but start as Secondary
kubectl apply -f failback/vrg_busybox_secondary.yaml --context $CLUSTER_TO

# 9b) wait for cluster1's VRG to have DataReady status
wait_for_vrg_condition_status $INDEX_DATA_READY True vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_TO

# 10) Delete PVCs on cluster2 so VRG can transition to Secondary. Sequence: 1) VR, 2) PVC (+PV)
# VRG deletion sequence part 1/2: delete VR
if [[ $(kubectl get vr/$VR_NAME -n $NAMESPACE --context $CLUSTER_FROM --no-headers | wc -l ) -gt 0 ]]; then
    kubectl delete vr/$VR_NAME -n $NAMESPACE --context $CLUSTER_FROM
fi

# VRG deletion sequence part 2/2: delete PVC
if [[ $(kubectl get pvc/$PVC_NAME -n $NAMESPACE --context $CLUSTER_FROM | wc -l) -gt 0 ]]; then
    # delete PVC
    kubectl delete pvc/$PVC_NAME -n $NAMESPACE --context $CLUSTER_FROM
fi

# wait for cluster2 VRG to transition to Secondary: 
wait_for_vrg_state "Secondary" vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_FROM

# 11) change cluster1 vrg to Primary
KUBE_EDITOR="sed -i 's/replicationState: secondary/replicationState: primary/g'" kubectl edit vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_TO
kubectl patch vrg/$VRG_NAME --type json -p '[{"op": add, "path":/spec/action, "value": Relocate}]' -n $NAMESPACE --context $CLUSTER_TO

# 12) wait for cluster1's VRG to have condition ClusterReadyStatus=True
wait_for_vrg_condition_status $INDEX_CLUSTER_DATA_READY True vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_TO

# 13) wait for cluster1's VRG to have condition DataReady=True
wait_for_vrg_condition_status $INDEX_DATA_READY True vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_TO

# wait for cluster1 to transition to Primary
wait_for_vrg_state "Primary" vrg/$VRG_NAME -n $NAMESPACE --context $CLUSTER_TO

# wait for new application to come online 
kubectl wait deployments/$DEPLOYMENT_NAME -n $NAMESPACE --for condition=available --timeout=60s --context=$CLUSTER_TO

# TODO: check backups/restores here

echo "failback successful"
