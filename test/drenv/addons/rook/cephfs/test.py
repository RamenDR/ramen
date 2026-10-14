# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from pathlib import Path

from drenv import kubectl

PACKAGE_DIR = Path(__file__).parent

PVC_NAME = "cephfs-pvc"
SNAP_NAME = "cephfs-snap"
NAMESPACE = "rook-cephfs-test"

_TEST_DATA = str(PACKAGE_DIR / "test-data")


def test(cluster):
    """
    Test CephFS PVC and snapshot provisioning.
    """
    print(f"Deploying pvc and snapshot on cluster '{cluster}'")
    kubectl.apply(
        "--kustomize",
        _TEST_DATA,
        context=cluster,
    )

    print(f"Waiting until pvc {PVC_NAME} is bound in cluster '{cluster}'")
    kubectl.wait(
        f"pvc/{PVC_NAME}",
        "--for=jsonpath={.status.phase}=Bound",
        f"--namespace={NAMESPACE}",
        context=cluster,
    )

    print(f"Waiting until snapshot {SNAP_NAME} is readyToUse in cluster '{cluster}'")
    kubectl.wait(
        f"volumesnapshot/{SNAP_NAME}",
        "--for=jsonpath={.status.readyToUse}=true",
        f"--namespace={NAMESPACE}",
        context=cluster,
    )

    print(f"Deleting pvc and snapshot on cluster '{cluster}'")
    kubectl.delete(
        "--kustomize",
        _TEST_DATA,
        "--ignore-not-found",
        context=cluster,
    )

    print(f"CephFS PVC and Snapshot provisioning on cluster '{cluster}' succeeded")
