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

# Currently cannot go below 1.10 due to csi addons related changes in scripts
ROOK_SRC="${ROOK_SRC:-"https://raw.githubusercontent.com/rook/rook/release-1.10/deploy/examples"}"

# Currently using main till a release is available with lastSyncTime updates
CSIADDON_SRC="${CSIADDON_SRC:-"https://raw.githubusercontent.com/csi-addons/kubernetes-csi-addons/main/deploy/controller"}"

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
	minikube start --profile="${PROFILE}"
    exit 0
fi

if [[ $1 == "create" ]]
then
        ### $1 == "create"
        # TODO: Check if already created and bail out!
        ## Preserve /var/lib/rook into existing vda1 disk ##
        # TODO: minikube instance is assumed to be kvm2 based, check it?
        minikube ssh "sudo mkdir -p /mnt/vda1/rook/ && sudo ln -sf /mnt/vda1/rook/ /var/lib/rook" --profile="${PROFILE}"
        
        ## Install rook-ceph ##
        kubectl apply -f "${ROOK_SRC}/common.yaml" --context="${PROFILE}"
        kubectl apply -f "${ROOK_SRC}/crds.yaml" --context="${PROFILE}"

        # Enable CSI addons (for volume replication)
        kubectl apply -f "${CSIADDON_SRC}/crds.yaml" --context="${PROFILE}"
        kubectl apply -f "${CSIADDON_SRC}/rbac.yaml" --context="${PROFILE}"
        kubectl apply -f "${CSIADDON_SRC}/setup-controller.yaml" --context="${PROFILE}"

        # Applicable from rook 1.10 onwards
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
        path: /data/CSI_ENABLE_CSIADDONS
        value: 'true'
      - op: add
        path: /data/ROOK_CSIADDONS_IMAGE
        value: quay.io/csiaddons/k8s-sidecar:latest
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
	exit 0
fi

usage
