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
CACHE_KEY = "addons/rook-cluster-1.20-2.yaml"

# The ceph, and ceph-csi images are very large (500m each), using larger
# timeout to avoid timeouts with flaky network.
TIMEOUT = 600

CSIADDONS_TIMEOUT = 300
CSIADDONS_ATTEMPTS = 3

# Plugin pods start with the operator, before the cluster exists. kubelet
# projects monitor addresses into the pods after Rook publishes them.
# Typically about one minute; fail faster than CephCluster image pull.
CSI_PLUGIN_TIMEOUT = 120
CSI_PLUGIN_POLL = 5

CSI_PLUGINS = [
    {
        "deploy": "deploy/rook-ceph.rbd.csi.ceph.com-ctrlplugin",
        "container": "csi-rbdplugin",
    },
    {
        "deploy": "deploy/rook-ceph.cephfs.csi.ceph.com-ctrlplugin",
        "container": "csi-cephfsplugin",
    },
]

# CSIAddonsNode resources are created by the csi-addons sidecar in the CSI
# driver pods. The sidecar registers the node after the pod is ready.
# The csi-addons CRD must be deployed before rook-cluster so the sidecar
# can register as soon as the pod starts.
#
# Without these resources, the VolumeReplication controller cannot find the
# CSI driver's replication client, causing VR reconciliation to fail with
# "no leader for the ControllerService".
CSIADDONS_NODES = [
    "daemonset-rook-ceph.rbd.csi.ceph.com-nodeplugin-csi-addons",
    "deployment-rook-ceph.rbd.csi.ceph.com-ctrlplugin",
    "deployment-rook-ceph.cephfs.csi.ceph.com-ctrlplugin",
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

    wait_for_csi_plugins(cluster)
    wait_for_csiaddons_nodes(cluster)


def wait_for_csi_plugins(cluster):
    """
    Wait until CSI provisioners can read this cluster's monitor list.

    CephCluster Ready does not cover CSI: plugins are a separate operator
    and are Running before the cluster exists. Creating a PVC before they
    have monitors fails with InvalidArgument, and the provisioner delays
    retries past the CephFS test timeout.
    """
    deadline = time.monotonic() + CSI_PLUGIN_TIMEOUT
    for plugin in CSI_PLUGINS:
        deploy = plugin["deploy"]
        print(f"Waiting until '{deploy}' has ceph monitors")
        while True:
            if _csi_plugin_has_monitors(cluster, plugin):
                break
            if time.monotonic() >= deadline:
                raise RuntimeError(f"Timeout waiting for ceph monitors in '{deploy}'")
            time.sleep(CSI_PLUGIN_POLL)


def _csi_plugin_has_monitors(cluster, plugin):
    deploy = plugin["deploy"]
    try:
        data = _csi_plugin_config(cluster, plugin)
    except commands.Error as e:
        print(f"failed to read config in '{deploy}': {e.error.rstrip()!r}")
        return False

    if not data.strip():
        return False

    try:
        monitors = _monitors(data)
    except json.JSONDecodeError as e:
        print(f"Invalid CSI config in '{deploy}': {e}: {data.rstrip()}")
        return False

    if monitors:
        print(f"Found ceph monitors in '{deploy}': {monitors}")
        return True

    print(f"CSI config in '{deploy}': {data.rstrip()}")
    return False


def _csi_plugin_config(cluster, plugin):
    # Missing file is expected until kubelet projects the ConfigMap. Return
    # empty instead of failing so the wait stays quiet. Other exec errors
    # still raise.
    return kubectl.exec(
        plugin["deploy"],
        "--namespace=rook-ceph",
        "-c",
        plugin["container"],
        "--",
        "sh",
        "-c",
        "if [ -f /etc/ceph-csi-config/config.json ]; then cat /etc/ceph-csi-config/config.json; fi",
        context=cluster,
    )


def _monitors(data):
    cfg = json.loads(data)
    if not isinstance(cfg, list) or not cfg:
        return []
    if not isinstance(cfg[0], dict):
        return []
    monitors = cfg[0].get("monitors", [])
    if not isinstance(monitors, list):
        return []
    return monitors


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
