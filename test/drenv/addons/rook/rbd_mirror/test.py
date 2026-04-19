# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import concurrent.futures
import json
import time

from drenv import kubectl

from .config import POOL_NAME, PACKAGE_DIR

NAMESPACE = "rbd-mirror-test"
PVC_NAME = "rbd-pvc"
VR_NAME = "vr-1m"

_DATA_DIR = PACKAGE_DIR / "test-data"


def test(cluster1, cluster2):
    """
    Test rbd mirroring between two clusters by replicating in both directions.
    """
    with concurrent.futures.ThreadPoolExecutor() as e:
        tests = [
            e.submit(test_volume_replication, cluster1, cluster2),
            e.submit(test_volume_replication, cluster2, cluster1),
        ]
        for t in concurrent.futures.as_completed(tests):
            t.result()


def test_volume_replication(primary, secondary):
    print(f"Deploying pvc {NAMESPACE}/{PVC_NAME} in cluster '{primary}'")
    kubectl.apply(f"--kustomize={_DATA_DIR}", context=primary)

    print(f"Waiting until pvc {NAMESPACE}/{PVC_NAME} is bound in cluster '{primary}'")
    kubectl.wait(
        f"pvc/{PVC_NAME}",
        "--for=jsonpath={.status.phase}=Bound",
        f"--namespace={NAMESPACE}",
        context=primary,
    )

    print(f"Deploying vr {NAMESPACE}/{VR_NAME} in cluster '{primary}'")
    kubectl.apply(
        f"--filename={_DATA_DIR / f'{VR_NAME}.yaml'}",
        f"--namespace={NAMESPACE}",
        context=primary,
    )

    print(f"Waiting until vr {NAMESPACE}/{VR_NAME} is completed in cluster '{primary}'")
    kubectl.wait(
        f"volumereplication/{VR_NAME}",
        "--for=condition=Completed",
        f"--namespace={NAMESPACE}",
        context=primary,
    )

    print(
        f"Waiting until vr {NAMESPACE}/{VR_NAME} state is primary in cluster '{primary}'"
    )
    kubectl.wait(
        f"volumereplication/{VR_NAME}",
        "--for=jsonpath={.status.state}=Primary",
        f"--namespace={NAMESPACE}",
        context=primary,
    )

    print(f"Looking up pvc {NAMESPACE}/{PVC_NAME} pv name in cluster '{primary}'")
    pv_name = kubectl.get(
        f"pvc/{PVC_NAME}",
        "--output=jsonpath={.spec.volumeName}",
        f"--namespace={NAMESPACE}",
        context=primary,
    )

    print(f"Looking up rbd image for pv {pv_name} in cluster '{primary}'")
    rbd_image = kubectl.get(
        f"pv/{pv_name}",
        "--output=jsonpath={.spec.csi.volumeAttributes.imageName}",
        context=primary,
    )

    print(f"rbd image {rbd_image} info in cluster '{primary}'")
    out = _rbd("info", rbd_image, cluster=primary)
    print(out)

    print(f"Waiting until rbd image {rbd_image} is created in cluster '{secondary}'")
    for i in range(60):
        time.sleep(1)
        out = _rbd("list", cluster=primary)
        if rbd_image in out:
            out = _rbd("info", rbd_image, cluster=primary)
            print(out)
            break
    else:
        raise RuntimeError(f"Timeout waiting for image {rbd_image}")

    print(f"vr {NAMESPACE}/{VR_NAME} info on primary cluster '{primary}'")
    kubectl.get(
        f"volumereplication/{VR_NAME}",
        "--output=yaml",
        f"--namespace={NAMESPACE}",
        context=primary,
    )

    print(f"rbd mirror image status in cluster '{primary}'")
    image_status = _rbd_mirror_image_status(primary, rbd_image)
    print(json.dumps(image_status, indent=2))

    print(f"Deleting vr {NAMESPACE}/{VR_NAME} in primary cluster '{primary}'")
    kubectl.delete(
        f"--filename={_DATA_DIR / f'{VR_NAME}.yaml'}",
        f"--namespace={NAMESPACE}",
        context=primary,
    )

    print(f"Deleting pvc {NAMESPACE}/{PVC_NAME} in primary cluster '{primary}'")
    kubectl.delete(f"--kustomize={_DATA_DIR}", context=primary)

    print(f"Replication from cluster '{primary}' to cluster '{secondary}' succeeded")


def _rbd(*args, cluster=None):
    """
    Run a rbd command using the ceph toolbox on the specified cluster.
    """
    return kubectl.exec(
        "deploy/rook-ceph-tools",
        "--namespace=rook-ceph",
        "--",
        "rbd",
        *args,
        f"--pool={POOL_NAME}",
        context=cluster,
    )


def _rbd_mirror_image_status(cluster, image):
    out = _rbd(
        "mirror",
        "image",
        "status",
        image,
        "--format=json",
        cluster=cluster,
    )
    status = json.loads(out)

    # Expand metrics json embedded in the peer description.
    for peer in status["peer_sites"]:
        desc = peer.get("description", "")
        if ", " in desc:
            state, metrics = desc.split(", ", 1)
            peer["description"] = {
                "state": state,
                "metrics": json.loads(metrics),
            }

    return status
