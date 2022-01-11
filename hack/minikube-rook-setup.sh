#!/bin/bash
set -x
set -e -o pipefail

scriptdir="$(dirname "$(realpath "$0")")"

## Variables
PROFILE="${PROFILE:-hub}"
POOL_NAME="minikube"

if test -z "${IMAGE_DIR}"; then
	IMAGE_DIR="$HOME/.minikube/images"
	mkdir -p "${IMAGE_DIR}"
fi
IMAGE_NAME="osd0"

ROOK_SRC="${ROOK_SRC:-"https://raw.githubusercontent.com/rook/rook/v1.6.5/cluster/examples/kubernetes/ceph/"}"

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
    local count=121
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
set +e
pool=$(virsh pool-dumpxml $POOL_NAME)
# error: failed to get pool 'minikube'
# error: Storage pool not found: no storage pool with matching name 'minikube'
set -e
pool_target=${pool#*<target>}
pool_target=${pool_target%</target>*}
pool_target_path=${pool_target#*<path>}
pool_target_path=${pool_target_path%</path>*}
pool_target_path_set()
{
	echo "${pool%<target>*}"\<target\>"${pool_target%<path>*}"\<path\>"$IMAGE_DIR"\</path\>"${pool_target#*</path>}"\</target\>"${pool#*</target>}" >/tmp/$$
	virsh pool-define /tmp/$$
	rm -f /tmp/$$
}
pool_target_path_set_unqualified()
{
	test 'Pool '$POOL_NAME' XML configuration edited.' = "$(\
	EDITOR=sed\ -i\ \''s,<path>.*</path>,<path>'$IMAGE_DIR'</path>,'\' virsh pool-edit $POOL_NAME \
	)"
}
case $pool_target_path in
"$IMAGE_DIR")
	;;
?*)
	pool_target_path_set
	virsh pool-destroy $POOL_NAME
	virsh pool-start $POOL_NAME
	;;
*)
	virsh pool-create-as --name "${POOL_NAME}" --type dir --target "${IMAGE_DIR}"
	;;
esac
unset -f pool_target_path_set pool_target_path_set_unqualified
unset -v pool pool_target pool_target_path
if ! virsh vol-info --pool "$POOL_NAME" "${IMAGE_NAME}-${PROFILE}"; then
	virsh vol-create-as --pool "${POOL_NAME}" --name "${IMAGE_NAME}-${PROFILE}" --capacity 32G --format qcow2
fi
if ! virsh domblkinfo --domain "$PROFILE" --device vdb; then
	sudo virsh attach-disk --domain "${PROFILE}" --source "${IMAGE_DIR}/${IMAGE_NAME}-${PROFILE}" --target vdb --persistent --driver qemu --subdriver qcow2 --targetbus virtio
fi
set +e
minikube ssh 'echo 1 | sudo tee /sys/bus/pci/rescan > /dev/null ; dmesg | grep virtio_blk' --profile="${PROFILE}"
# ssh: Process exited with status 1
set -e

## Install rook-ceph ##
kubectl apply -f "${ROOK_SRC}/common.yaml" --context="${PROFILE}"
kubectl apply -f "${ROOK_SRC}/crds.yaml" --context="${PROFILE}"
# Fetch operator.yaml and enable CSI volume replication
set -- "$(mktemp --directory)"
cat <<a >"$1"/kustomization.yaml
resources:
  - ${ROOK_SRC}/operator.yaml
patchesJson6902:
  - target:
      kind: ConfigMap
      name: rook-ceph-operator-config
      namespace: rook-ceph
    patch: |-
      - op: add
        path: /data/CSI_ENABLE_VOLUME_REPLICATION
        value: 'true'
      - op: add
        path: /data/CSI_VOLUME_REPLICATION_IMAGE
        value: quay.io/csiaddons/volumereplication-operator:latest
      - op: add
        path: /data/CSI_ENABLE_OMAP_GENERATOR
        value: 'true'
      - op: add
        path: /data/ROOK_CSI_ALLOW_UNSUPPORTED_VERSION
        value: 'true'
      - op: add
        path: /data/ROOK_CSI_CEPH_IMAGE
        value: quay.io/cephcsi/cephcsi:canary
a
kubectl --context "$PROFILE" apply -k "$1"
rm -rf "$1"
set --
# Create a dev ceph cluster
kubectl apply -f "${scriptdir}/dev-rook-cluster.yaml" --context="${PROFILE}"
kubectl apply -f "${ROOK_SRC}/toolbox.yaml" --context="${PROFILE}"

# Create a mirroring enabled RBD pool
kubectl apply -f "${scriptdir}/dev-rook-rbdpool.yaml" --context="${PROFILE}"

# Create a StorageClass
kubectl apply -f "${scriptdir}"/dev-rook-sc.yaml --context="${PROFILE}"

# Ensure the pool is created and ready
wait_for_condition "Ready" kubectl get cephblockpool -n rook-ceph replicapool -o jsonpath='{.status.phase}' --context="${PROFILE}"
wait_for_condition "pool-peer-token-replicapool" kubectl get cephblockpool -n rook-ceph replicapool -o jsonpath='{.status.info.rbdMirrorBootstrapPeerSecretName}' --context="${PROFILE}"
echo Setup succesful!
