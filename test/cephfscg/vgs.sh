#!/bin/bash -e
cd "$(dirname "$0")"

SNAPSHOT_VERSION=${SNAPSHOT_VERSION:-"v7.0.2"}

SNAPSHOTTER_URL="https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/${SNAPSHOT_VERSION}"

# controller
SNAPSHOT_RBAC="${SNAPSHOTTER_URL}/deploy/kubernetes/snapshot-controller/rbac-snapshot-controller.yaml"

# volumegroupsnapshot CRD
VOLUME_GROUP_SNAPSHOTCLASS="${SNAPSHOTTER_URL}/client/config/crd/groupsnapshot.storage.k8s.io_volumegroupsnapshotclasses.yaml"
VOLUME_GROUP_SNAPSHOT_CONTENT="${SNAPSHOTTER_URL}/client/config/crd/groupsnapshot.storage.k8s.io_volumegroupsnapshotcontents.yaml"
VOLUME_GROUP_SNAPSHOT="${SNAPSHOTTER_URL}/client/config/crd/groupsnapshot.storage.k8s.io_volumegroupsnapshots.yaml"

echo "-----start to handle dr1------"
kubectl --context dr1 apply -f "${VOLUME_GROUP_SNAPSHOTCLASS}"
kubectl --context dr1 apply -f "${VOLUME_GROUP_SNAPSHOT_CONTENT}"
kubectl --context dr1 apply -f "${VOLUME_GROUP_SNAPSHOT}"
kubectl --context dr1 apply -f "${SNAPSHOT_RBAC}"
minikube image load -p dr1 snapshot-controller.tar
minikube image load -p dr1 csi-snapshotter.tar
minikube image load -p dr1 cephcsi.tar
kubectl --context dr1 apply -f snapshot-controller-runner-rbac.yaml
kubectl --context dr1 apply -f csi-cephfsplugin-provisioner.rook-ceph.yaml
kubectl --context dr1 apply -f csi-cephfsplugin.rook-ceph.yaml
kubectl --context dr1 apply -f snapshot-controller.kube-system.yaml
kubectl --context dr1 apply -f vgsc.yaml

echo "-----start to handle dr2------"
kubectl --context dr2 apply -f "${VOLUME_GROUP_SNAPSHOTCLASS}"
kubectl --context dr2 apply -f "${VOLUME_GROUP_SNAPSHOT_CONTENT}"
kubectl --context dr2 apply -f "${VOLUME_GROUP_SNAPSHOT}"
kubectl --context dr2 apply -f "${SNAPSHOT_RBAC}"
minikube image load -p dr2 snapshot-controller.tar
minikube image load -p dr2 csi-snapshotter.tar
minikube image load -p dr2 cephcsi.tar
kubectl --context dr2 apply -f snapshot-controller-runner-rbac.yaml
kubectl --context dr2 apply -f csi-cephfsplugin-provisioner.rook-ceph.yaml
kubectl --context dr2 apply -f csi-cephfsplugin.rook-ceph.yaml
kubectl --context dr2 apply -f snapshot-controller.kube-system.yaml
kubectl --context dr2 apply -f vgsc.yaml

echo "-----finish handle dr1&2------"

# kubectl --context dr1 patch deployment snapshot-controller -n kube-system -p '{"spec":{"template":{"spec":{"containers":[{"name":"volume-snapshot-controller","image":"localhost/snapshot-controller:latest"]}]}}}}'
# kubectl --context dr1 patch deployment csi-cephfsplugin-provisioner -n rook-ceph -p '{"spec":{"template":{"spec":{"containers":[{"name":"csi-snapshotter","image":"localhost/csi-snapshotter:latest"]}]}}}}'
# kubectl --context dr1 patch deployment csi-cephfsplugin-provisioner -n rook-ceph -p '{"spec":{"template":{"spec":{"containers":[{"name":"csi-cephfsplugin","image":"quay.io/cephcsi/cephcsi:canary"]}]}}}}'
# kubectl --context dr1 patch daemonset  csi-cephfsplugin -n rook-ceph -p '{"spec":{"template":{"spec":{"containers":[{"name":"csi-cephfsplugin","image":"quay.io/cephcsi/cephcsi:canary"]}]}}}}'
#add args and change hte image
#kcdr1 edit deployment snapshot-controller -n kube-system
#kcdr1 edit deployment csi-cephfsplugin-provisioner -n rook-ceph

# buil exteral-snapshoter and replace image in rook and kube-system
# edit image to quay.io/cephcsi/cephcsi:canary
#kcdr1 edit daemonset -n rook-ceph csi-cephfsplugin
#kcdr1 edit deployment csi-cephfsplugin-provisioner -n rook-ceph


#localhost/snapshot-controller:latest
#localhost/csi-snapshotter:latest
#quay.io/cephcsi/cephcsi:canary