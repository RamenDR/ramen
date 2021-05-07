#!/bin/bash
set -x
set -e -o pipefail

scriptdir="$(dirname "$(realpath "$0")")"

## Variables
PROFILE="${PROFILE:-hub}"
POOL_NAME="minikube"

IMAGE_DIR="${IMAGE_DIR:-"/var/lib/libvirt/images"}"
IMAGE_NAME="osd0"

ROOK_SRC="${ROOK_SRC:-"https://raw.githubusercontent.com/rook/rook/release-1.6/cluster/examples/kubernetes/ceph/"}"

## Usage
usage()
{
	set +x
	echo "Usage:"
	echo "  $0 {create|start|stop|delete}"
	echo "  Available environment variables:"
	echo "    minikube PROFILE ${PROFILE}"
	echo "    minikube kvm2 image dir IMAGE_DIR ${IMAGE_DIR}"
	echo "    rook source ROOK_SRC ${ROOK_SRC}"
	exit 1
}

function wait_for_ssh() {
    local tries=60
    while ((tries > 0)); do
        if minikube ssh "echo connected" --profile="$1" &>/dev/null; then
            return 0
        fi
        tries=$((tries - 1))
        sleep 1
    done
    echo ERROR: ssh did not come up >&2
    exit 1
}

function wait_for_condition() {
    local count=15
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

set +x
echo "Using environment:"
echo "  minikube PROFILE ${PROFILE}"
echo "  minikube kvm2 image dir IMAGE_DIR ${IMAGE_DIR}"
echo "  rook source ROOK_SRC ${ROOK_SRC}"
set -x

if [[ $1 == "delete" ]]
then
	minikube delete --profile="${PROFILE}"
	virsh vol-delete --pool "${POOL_NAME}" "${IMAGE_NAME}-${PROFILE}"
    exit 0
fi

if [[ $1 == "stop" ]]; then
	minikube stop --profile="${PROFILE}"
    exit 0
fi

if [[ $1 == "start" ]]; then
	sudo virsh start "${PROFILE}"
	wait_for_ssh "$PROFILE"
	minikube ssh "sudo mkdir -p /mnt/vda1/rook/ && sudo ln -sf /mnt/vda1/rook/ /var/lib/rook" --profile="${PROFILE}"
	minikube start --profile="${PROFILE}"
    exit 0
fi

if [[ $1 != "create" ]]; then
	usage
fi

### $1 == "create"
# TODO: Check if already created and bail out!
## Preserve /var/lib/rook into existing vda1 disk ##
# TODO: minikube instance is assumed to be kvm2 based, check it?
minikube ssh "sudo mkdir -p /mnt/vda1/rook/ && sudo ln -sf /mnt/vda1/rook/ /var/lib/rook" --profile="${PROFILE}"

## Create and attach an OSD disk for Ceph ##
if [[ $(virsh pool-list | grep -wc "${POOL_NAME}") == 0 ]]; then
	virsh pool-create-as --name "${POOL_NAME}" --type dir --target "${IMAGE_DIR}"
fi
virsh vol-create-as --pool "${POOL_NAME}" --name "${IMAGE_NAME}-${PROFILE}" --capacity 32G --format qcow2
sudo virsh attach-disk --domain "${PROFILE}" --source "${IMAGE_DIR}/${IMAGE_NAME}-${PROFILE}" --target vdb --persistent --driver qemu --subdriver qcow2 --targetbus virtio
minikube ssh 'echo 1 | sudo tee /sys/bus/pci/rescan > /dev/null ; dmesg | grep virtio_blk' --profile="${PROFILE}"

## Install rook-ceph ##
kubectl apply -f "${ROOK_SRC}/common.yaml" --context="${PROFILE}"
kubectl apply -f "${ROOK_SRC}/crds.yaml" --context="${PROFILE}"
# Fetch operator.yaml and enable CSI volume replication
TMP_OPERATOR_YAML=$(mktemp --suffix .yaml)
wget "${ROOK_SRC}/operator.yaml" -O "${TMP_OPERATOR_YAML}"
sed -e 's,CSI_ENABLE_VOLUME_REPLICATION: "false",CSI_ENABLE_VOLUME_REPLICATION: "true",' -i "${TMP_OPERATOR_YAML}"
sed -e 's,# CSI_VOLUME_REPLICATION_IMAGE: "quay.io/csiaddons/volumereplication-operator:v0.1.0",CSI_VOLUME_REPLICATION_IMAGE: "quay.io/csiaddons/volumereplication-operator:latest",' -i "${TMP_OPERATOR_YAML}"
sed -e 's,# CSI_ENABLE_OMAP_GENERATOR: "false",CSI_ENABLE_OMAP_GENERATOR: "true",' -i "${TMP_OPERATOR_YAML}"
kubectl apply -f "${TMP_OPERATOR_YAML}" --context="${PROFILE}"
rm -f "${TMP_OPERATOR_YAML}"
# Create a dev ceph cluster
kubectl apply -f "${scriptdir}/dev-rook-cluster.yaml" --context="${PROFILE}"
kubectl apply -f "${ROOK_SRC}/toolbox.yaml" --context="${PROFILE}"

# Create a mirroring enabled RBD pool
kubectl apply -f "${scriptdir}/dev-rook-rbdpool.yaml" --context="${PROFILE}"

# Ensure the pool is created and ready
wait_for_condition "Ready" kubectl get cephblockpool -n rook-ceph replicapool -o jsonpath='{.status.phase}' --context="${PROFILE}"
wait_for_condition "pool-peer-token-replicapool" kubectl get cephblockpool -n rook-ceph replicapool -o jsonpath='{.status.info.rbdMirrorBootstrapPeerSecretName}' --context="${PROFILE}"
echo Setup succesful!
