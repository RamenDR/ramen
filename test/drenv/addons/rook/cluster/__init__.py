# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import json
import time
from pathlib import Path

import yaml

from drenv import cache as _cache
from drenv import commands
from drenv import kubectl

PACKAGE_DIR = Path(__file__).parent
CACHE_KEY = "addons/rook-cluster-1.18.yaml"

# The ceph, and ceph-csi images are very large (500m each), using larger
# timeout to avoid timeouts with flaky network.
TIMEOUT = 600

# CSI driver components created by the rook operator as part of the
# CephCluster reconciliation.
CSI_COMPONENTS = [
    {"kind": "daemonset", "name": "csi-rbdplugin"},
    {"kind": "daemonset", "name": "csi-cephfsplugin"},
    {"kind": "deployment", "name": "csi-rbdplugin-provisioner"},
    {"kind": "deployment", "name": "csi-cephfsplugin-provisioner"},
]

CSIADDONS_TIMEOUT = 300
CSIADDONS_ATTEMPTS = 3

# CSIAddonsNode resources are created by the csi-addons sidecar in the CSI
# driver pods. The sidecar registers the node after the pod is ready.
# The csi-addons CRD must be deployed before rook-cluster so the sidecar
# can register as soon as the pod starts.
#
# Without these resources, the VolumeReplication controller cannot find the
# CSI driver's replication client, causing VR reconciliation to fail with
# "no leader for the ControllerService".
CSIADDONS_NODES = [
    "daemonset-csi-rbdplugin",
    "deployment-csi-rbdplugin-provisioner",
    "deployment-csi-cephfsplugin-provisioner",
]


def start(cluster):
    """
    Deploy the rook ceph cluster and wait until it is ready.
    """
    deploy(cluster)
    wait(cluster)


def cache():
    """
    Refresh the cached kustomization yaml.
    """
    _cache.refresh(str(PACKAGE_DIR), CACHE_KEY)


def deploy(cluster):
    print("Deploying rook ceph cluster")
    path = _cache.get(str(PACKAGE_DIR), CACHE_KEY)
    kubectl.apply("--filename", path, context=cluster)


def wait(cluster):
    print("Waiting until cephcluster 'rook-ceph/my-cluster' exists")
    kubectl.wait(
        "cephcluster/my-cluster",
        "--for=create",
        "--namespace=rook-ceph",
        context=cluster,
    )
    print("Waiting until cephcluster 'rook-ceph/my-cluster' is ready")
    kubectl.wait(
        "cephcluster/my-cluster",
        "--for=jsonpath={.status.phase}=Ready",
        "--namespace=rook-ceph",
        timeout=TIMEOUT,
        context=cluster,
    )

    out = kubectl.get(
        "cephcluster/my-cluster",
        "--output=jsonpath={.status}",
        "--namespace=rook-ceph",
        context=cluster,
    )
    info = {"ceph cluster status": json.loads(out)}
    print(yaml.dump(info, sort_keys=False))

    for comp in CSI_COMPONENTS:
        print(f"Waiting until {comp['kind']} 'rook-ceph/{comp['name']}' is rolled out")
        kubectl.rollout(
            "status",
            f"{comp['kind']}/{comp['name']}",
            "--namespace=rook-ceph",
            context=cluster,
        )

    wait_for_csiaddons_nodes(cluster)


def wait_for_csiaddons_nodes(cluster):
    """
    Wait for CSIAddonsNode resources to report status.state=Connected.

    The csi-addons sidecar deletes and recreates the CSIAddonsNode resource
    when the CSI driver pod restarts. This can cause kubectl wait to fail
    with NotFound if the resource is deleted between the wait_for and
    kubectl.wait calls. We retry to handle this race.
    """
    deadline = time.monotonic() + CSIADDONS_TIMEOUT

    for suffix in CSIADDONS_NODES:
        name = f"{cluster}-rook-ceph-{suffix}"
        resource = f"csiaddonsnodes.csiaddons.openshift.io/{name}"

        for attempt in range(1, CSIADDONS_ATTEMPTS + 1):
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise RuntimeError(f"Timeout waiting for {resource}")

            print(f"Waiting until '{resource}' exists")
            kubectl.wait(
                resource,
                "--for=create",
                "--namespace=rook-ceph",
                timeout=remaining,
                context=cluster,
            )

            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise RuntimeError(f"Timeout waiting for {resource}")

            print(f"Waiting until '{resource}' status.state is Connected")
            try:
                kubectl.wait(
                    resource,
                    "--for=jsonpath={.status.state}=Connected",
                    "--namespace=rook-ceph",
                    timeout=remaining,
                    context=cluster,
                )
                break
            except commands.Error:
                if attempt == CSIADDONS_ATTEMPTS:
                    raise
                print(
                    f"Retrying wait for '{resource}' ({attempt}/{CSIADDONS_ATTEMPTS})"
                )
