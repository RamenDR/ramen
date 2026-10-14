# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import json
import string
from pathlib import Path

import yaml

from drenv import kubectl

PACKAGE_DIR = Path(__file__).parent

BUILTIN_MGR_POOL = "builtin-mgr"
POOL_NAMES = ["replicapool", "replicapool-2"]


def start(cluster):
    """
    Deploy RBD pools, storage classes, and snapshot class, and wait until
    the pool is ready.
    """
    deploy(cluster)
    wait(cluster)


def deploy(cluster):
    storage_classes = [
        {"name": "rook-ceph-block", "pool": POOL_NAMES[0]},
        {"name": "rook-ceph-block-2", "pool": POOL_NAMES[1]},
    ]

    print("Creating StorageClasses")
    for storage_class in storage_classes:
        template = _template("storage-class.yaml")
        yaml_str = template.substitute(
            cluster=cluster, name=storage_class["name"], pool=storage_class["pool"]
        )
        kubectl.apply("--filename=-", input=yaml_str, context=cluster)

    # RADOS pool IDs are assigned in create order. CSI volumeHandles
    # embed the replica pool ID, and RBD failover restores the source
    # handle, so both clusters need the same replica pool IDs. Since
    # Rook 1.20, .mgr is created around the same time as user pools,
    # so wait until .mgr has an ID before creating replica pools.
    wait_until_pool_has_id(cluster, BUILTIN_MGR_POOL)

    print("Creating RBD pools")
    for pool in POOL_NAMES:
        template = _template("replica-pool.yaml")
        yaml_str = template.substitute(cluster=cluster, name=pool)
        kubectl.apply("--filename=-", input=yaml_str, context=cluster)

    print("Creating SnapshotClass")
    template = _template("snapshot-class.yaml")
    yaml_str = template.substitute(cluster=cluster, scname="rook-ceph-block")
    kubectl.apply("--filename=-", input=yaml_str, context=cluster)


def wait_until_pool_has_id(cluster, name):
    print(f"Waiting until cephblockpool 'rook-ceph/{name}' exists")
    kubectl.wait(
        f"cephblockpool/{name}",
        "--for=create",
        "--namespace=rook-ceph",
        context=cluster,
    )
    print(f"Waiting until cephblockpool 'rook-ceph/{name}' has a poolID")
    kubectl.wait(
        f"cephblockpool/{name}",
        "--for=jsonpath={.status.poolID}",
        "--namespace=rook-ceph",
        context=cluster,
    )
    pool_id = kubectl.get(
        f"cephblockpool/{name}",
        "--output=jsonpath={.status.poolID}",
        "--namespace=rook-ceph",
        context=cluster,
    )
    print(f"cephblockpool 'rook-ceph/{name}' poolID={pool_id}")


def wait(cluster):
    print("Waiting until cephblockpool 'rook-ceph/replicapool' exists")
    kubectl.wait(
        "cephblockpool/replicapool",
        "--for=create",
        "--namespace=rook-ceph",
        context=cluster,
    )
    print("Waiting until cephblockpool 'rook-ceph/replicapool' is ready")
    kubectl.wait(
        "cephblockpool/replicapool",
        "--for=jsonpath={.status.phase}=Ready",
        "--namespace=rook-ceph",
        context=cluster,
    )

    print("Waiting for replica pool peer token")
    kubectl.wait(
        "cephblockpool/replicapool",
        "--for=jsonpath={.status.info.rbdMirrorBootstrapPeerSecretName}=pool-peer-token-replicapool",
        "--namespace=rook-ceph",
        context=cluster,
    )

    out = kubectl.get(
        "cephblockpool/replicapool",
        "--output=jsonpath={.status}",
        "--namespace=rook-ceph",
        context=cluster,
    )
    info = {"ceph pool status": json.loads(out)}
    print(yaml.dump(info, sort_keys=False))


def _template(name):
    path = PACKAGE_DIR / name
    with open(path) as f:
        return string.Template(f.read())
