# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import string
import subprocess
import sys

import drenv
from drenv import cache as _cache
from drenv import commands
from drenv import kubectl

from drenv.addons.rook.pool import PACKAGE_DIR as _POOL_DIR

from .config import (
    BASE_URL,
    CACHE_KEY,
    CREATE_EXTERNAL_CLUSTER_RESOURCES_URL,
    IMPORT_SCRIPT_URL,
    IMPORT_STORAGE_CLASS_NAMES,
    PACKAGE_DIR,
    ROOK_CEPH_NAMESPACE,
    STORAGE_CLASSES,
)

_DATA_DIR = PACKAGE_DIR / "start-data"


def start(cluster, external_storage_cluster):
    deploy(cluster, external_storage_cluster)
    wait(cluster)


def cache(log=None):
    _cache.refresh_file(CACHE_KEY, IMPORT_SCRIPT_URL)


def deploy(cluster, external_storage_cluster):
    print("Deploying rook ceph cluster in external mode")
    kustomization_path = _DATA_DIR / "kustomization.yaml"
    with drenv.kustomization(
        str(kustomization_path),
        base_url=BASE_URL,
    ) as kustomization:
        kubectl.apply("--kustomize", kustomization, context=cluster)

    _connect_to_external_storage_cluster(cluster, external_storage_cluster)
    _delete_import_storage_classes(cluster)
    _create_storage_classes(cluster, external_storage_cluster)
    _create_network_fence_classes(cluster, external_storage_cluster)


def wait(cluster):
    print("Waiting until rook ceph external cluster is connected")
    _wait_for_ceph_cluster_phase(cluster, "Connected")


def _connect_to_external_storage_cluster(managed_cluster, external_storage_cluster):
    cluster_details = _get_cluster_details_from_external_storage_cluster(
        external_storage_cluster
    )
    env = os.environ.copy()

    env["NAMESPACE"] = ROOK_CEPH_NAMESPACE
    env["KUBECONTEXT"] = managed_cluster

    for line in cluster_details.split("\n"):
        if line.startswith("export "):
            _, rest = line.split("export ", 1)
            k, v = rest.split("=", 1)
            env[k] = v

    path = _cache.get_file(CACHE_KEY, IMPORT_SCRIPT_URL)

    result = subprocess.run(
        ["/bin/bash", path], capture_output=True, encoding="utf-8", env=env
    )

    if result.returncode != 0:
        print("Failed to run script: " + result.stderr)
        sys.exit(1)


def _delete_import_storage_classes(managed_cluster):
    print(
        f"Deleting imported StorageClasses on '{managed_cluster}': "
        f"{', '.join(IMPORT_STORAGE_CLASS_NAMES)}"
    )
    for sc_name in IMPORT_STORAGE_CLASS_NAMES:
        kubectl.delete(
            "storageclass", sc_name, "--ignore-not-found", context=managed_cluster
        )


def _create_storage_classes(managed_cluster, external_storage_cluster):
    print(
        f"Creating StorageClasses on '{managed_cluster}', "
        f"with storageid matching that of StorageClasses on cluster "
        f"'{external_storage_cluster}'"
    )
    template = drenv.template(str(_POOL_DIR / "storage-class.yaml"))
    for sc in STORAGE_CLASSES:
        yaml_str = template.substitute(
            cluster=external_storage_cluster,
            name=sc["name"],
            pool=sc["pool"],
        )
        kubectl.apply("--filename=-", input=yaml_str, context=managed_cluster)


def _create_network_fence_classes(managed_cluster, external_storage_cluster):
    print(
        f"Creating NetworkFenceClasses on '{managed_cluster}', "
        f"with storageid matching StorageClasses on cluster "
        f"'{external_storage_cluster}'"
    )
    template = _template("networkfenceclass.yaml")
    for sc in STORAGE_CLASSES:
        yaml_str = template.substitute(
            cluster=external_storage_cluster,
            name=sc["name"],
        )
        kubectl.apply("--filename=-", input=yaml_str, context=managed_cluster)


def _get_cluster_details_from_external_storage_cluster(external_storage_cluster):
    _wait_for_ceph_cluster_phase(external_storage_cluster, "Ready")
    podname = _get_ceph_toolbox_pod_name(external_storage_cluster)
    return _get_rook_ceph_external_cluster_details(podname, external_storage_cluster)


def _get_ceph_toolbox_pod_name(cluster):
    return kubectl.get(
        "pod",
        "-l app=rook-ceph-tools",
        "--output=jsonpath={.items[0].metadata.name}",
        f"--namespace={ROOK_CEPH_NAMESPACE}",
        context=cluster,
    )


def _get_rook_ceph_external_cluster_details(podname, cluster):
    try:
        kubectl.exec(
            podname,
            f"--namespace={ROOK_CEPH_NAMESPACE}",
            "--",
            "curl",
            "-o",
            "/tmp/create-external-cluster-resources.py",
            CREATE_EXTERNAL_CLUSTER_RESOURCES_URL,
            context=cluster,
        )
    except commands.Error as e:
        print("kubectl.exec failed:", e)
        raise

    try:
        out = kubectl.exec(
            podname,
            f"--namespace={ROOK_CEPH_NAMESPACE}",
            "--",
            "python3",
            "/tmp/create-external-cluster-resources.py",
            "--format",
            "bash",
            "--rbd-data-pool-name",
            "replicapool",
            "--skip-monitoring-endpoint",
            context=cluster,
        )
    except commands.Error as e:
        print("kubectl.exec failed:", e)
        raise

    return out


def _wait_for_ceph_cluster_phase(cluster, phase):
    print("Waiting until rook ceph external cluster is connected")
    kubectl.wait(
        "cephcluster/my-cluster",
        "--for=create",
        "--namespace=rook-ceph",
        context=cluster,
    )
    kubectl.wait(
        "cephcluster/my-cluster",
        f"--for=jsonpath={{.status.phase}}={phase}",
        "--namespace=rook-ceph",
        "--timeout=300s",
        context=cluster,
    )


def _template(name):
    path = _DATA_DIR / name
    with open(path) as f:
        return string.Template(f.read())
