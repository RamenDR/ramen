#!/bin/bash
set -x
set -e -o pipefail

## Variables
PRIMARY_CLUSTER="${PRIMARY_CLUSTER:-hub}"
SECONDARY_CLUSTER="${SECONDARY_CLUSTER:-cluster1}"
STORAGECLASS_NAME="rook-ceph-block"
PVC_NAME="rbd-pvc"

function wait_for_condition() {
    local count=61
    local condition=${1}
    local result
    shift

    while ((count > 0)); do
        result=$("${@}")
        if [[ "$result" == "$condition" ]]; then
            return 0
        fi
        count=$((count - 1))
        sleep 5
    done

    echo "Failed to meet $condition for command $*"
    exit 1
}
# shellcheck source=./until_true_or_n.sh disable=1091
. "$(dirname "$0")"/until_true_or_n.sh

## Usage
usage()
{
	set +x
	echo "Usage:"
	echo "  $0"
	echo "  Available environment variables:"
	echo "    minikube primary cluster PRIMARY_CLUSTER ${PRIMARY_CLUSTER}"
	echo "    minikube secondary cluster SECONDARY_CLUSTER ${SECONDARY_CLUSTER}"
	exit 1
}

cat <<EOF | kubectl --context="${PRIMARY_CLUSTER}" apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: "${PVC_NAME}"
spec:
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: "${STORAGECLASS_NAME}"
EOF

wait_for_condition "Bound" kubectl get pvc "${PVC_NAME}" --context="${PRIMARY_CLUSTER}" -o jsonpath='{.status.phase}'

cat <<EOF | kubectl --context="${PRIMARY_CLUSTER}" apply -f -
apiVersion: replication.storage.openshift.io/v1alpha1
kind: VolumeReplication
metadata:
  name: vr-sample
spec:
  volumeReplicationClass: vrc-sample
  replicationState: primary
  dataSource:
    kind: PersistentVolumeClaim
    name: "${PVC_NAME}"
  autoResync: true
EOF

wait_for_condition "Primary" kubectl get volumereplication vr-sample --context="${PRIMARY_CLUSTER}" -o jsonpath='{.status.state}'

RBD_IMAGE_NAME=$(kubectl --context="${PRIMARY_CLUSTER}" get pv/"$(kubectl --context="${PRIMARY_CLUSTER}" get pvc/"${PVC_NAME}" -o jsonpath="{.spec.volumeName}")" -o jsonpath="{.spec.csi.volumeAttributes.imageName}")
echo RBD_IMAGE_NAME is "${RBD_IMAGE_NAME}"

CEPH_TOOLBOX_POD=$(kubectl --context="${PRIMARY_CLUSTER}" -n rook-ceph get pods -l  app=rook-ceph-tools -o jsonpath='{.items[0].metadata.name}')
echo CEPH_TOOLBOX_POD on primary cluster is "$CEPH_TOOLBOX_POD"

kubectl --context="${PRIMARY_CLUSTER}" -n rook-ceph exec "${CEPH_TOOLBOX_POD}" -- rbd info "${RBD_IMAGE_NAME}" --pool=replicapool

CEPH_TOOLBOX_POD=$(kubectl --context="${SECONDARY_CLUSTER}" -n rook-ceph get pods -l  app=rook-ceph-tools -o jsonpath='{.items[0].metadata.name}')
echo CEPH_TOOLBOX_POD on secondary cluster is "$CEPH_TOOLBOX_POD"

until_true_or_n 300 kubectl --context="${SECONDARY_CLUSTER}" -n rook-ceph exec "${CEPH_TOOLBOX_POD}" -- rbd info "${RBD_IMAGE_NAME}" --pool=replicapool

kubectl --context="${PRIMARY_CLUSTER}" get volumereplication vr-sample -o yaml

kubectl --context="${PRIMARY_CLUSTER}" delete volumereplication vr-sample
kubectl --context="${PRIMARY_CLUSTER}" delete pvc "${PVC_NAME}"

echo "Tests succesful!"
