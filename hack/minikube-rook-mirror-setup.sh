#!/bin/bash
set -x
set -e -o pipefail
scriptdir="$(dirname "$(realpath "$0")")"

## Variables
PRIMARY_CLUSTER="${PRIMARY_CLUSTER:-hub}"
SECONDARY_CLUSTER="${SECONDARY_CLUSTER:-cluster1}"
POOL_NAME="replicapool"

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

SECONDARY_CLUSTER_PEER_TOKEN_SECRET_NAME=$(kubectl get cephblockpools.ceph.rook.io "${POOL_NAME}" --context="${SECONDARY_CLUSTER}" -nrook-ceph -o jsonpath='{.status.info.rbdMirrorBootstrapPeerSecretName}')
SECONDARY_CLUSTER_SECRET=$(kubectl get secret -n rook-ceph "${SECONDARY_CLUSTER_PEER_TOKEN_SECRET_NAME}" --context="${SECONDARY_CLUSTER}" -o jsonpath='{.data.token}'| base64 -d)
SECONDARY_CLUSTER_SITE_NAME=$(kubectl get cephblockpools.ceph.rook.io "${POOL_NAME}" --context="${SECONDARY_CLUSTER}" -nrook-ceph -o jsonpath='{.status.mirroringInfo.site_name}')

echo SECONDARY_CLUSTER_PEER_TOKEN_SECRET_NAME is "$SECONDARY_CLUSTER_PEER_TOKEN_SECRET_NAME"
echo Token for the secondary cluster is "$SECONDARY_CLUSTER_SECRET"
echo SECONDARY_CLUSTER_SITE_NAME is "${SECONDARY_CLUSTER_SITE_NAME}"

MIRROR_SECRET_YAML=$(mktemp --suffix .yaml)
cp "${scriptdir}/rook-mirror-secret-template.yaml" "${MIRROR_SECRET_YAML}"
sed -e "s,<name>,${SECONDARY_CLUSTER_SITE_NAME}," -i "${MIRROR_SECRET_YAML}"
sed -e "s,<pool>,${POOL_NAME}," -i "${MIRROR_SECRET_YAML}"
sed -e "s,<token>,${SECONDARY_CLUSTER_SECRET}," -i "${MIRROR_SECRET_YAML}"
kubectl apply -f "${MIRROR_SECRET_YAML}" --context="${PRIMARY_CLUSTER}"
rm -f "${MIRROR_SECRET_YAML}"

rbd_pool_patch="{\"spec\":{\"mirroring\":{\"peers\":{\"secretNames\":[\"${SECONDARY_CLUSTER_SITE_NAME}\"]}}}}"
kubectl patch CephBlockPool ${POOL_NAME} -n rook-ceph --type merge --patch "${rbd_pool_patch}" --context "$PRIMARY_CLUSTER"

cat <<EOF | kubectl --context="${PRIMARY_CLUSTER}" apply -f -
apiVersion: ceph.rook.io/v1
kind: CephRBDMirror
metadata:
  name: my-rbd-mirror
  namespace: rook-ceph
spec:
  count: 1
EOF

#kubectl wait deployments -n rook-ceph --for condition=available rook-ceph-rbd-mirror-a --timeout=60s --context="${PRIMARY_CLUSTER}"

wait_for_condition "OK" kubectl get cephblockpools.ceph.rook.io replicapool --context="${PRIMARY_CLUSTER}" -nrook-ceph -o jsonpath='{.status.mirroringStatus.summary.daemon_health}'
echo RBD mirror daemon health OK
wait_for_condition "OK" kubectl get cephblockpools.ceph.rook.io replicapool --context="${PRIMARY_CLUSTER}" -nrook-ceph -o jsonpath='{.status.mirroringStatus.summary.health}'
echo RBD mirror status health OK
wait_for_condition "OK" kubectl get cephblockpools.ceph.rook.io replicapool --context="${PRIMARY_CLUSTER}" -nrook-ceph -o jsonpath='{.status.mirroringStatus.summary.image_health}'
echo RBD mirror image summary health OK

cat <<EOF | kubectl --context="${PRIMARY_CLUSTER}" apply -f -
apiVersion: replication.storage.openshift.io/v1alpha1
kind: VolumeReplicationClass
metadata:
  name: vrc-sample
spec:
  provisioner: rook-ceph.rbd.csi.ceph.com
  parameters:
    replication.storage.openshift.io/replication-secret-name: rook-csi-rbd-provisioner
    replication.storage.openshift.io/replication-secret-namespace: rook-ceph
    schedulingInterval: 1m
EOF

echo "Setup successfull!"

exit 0
