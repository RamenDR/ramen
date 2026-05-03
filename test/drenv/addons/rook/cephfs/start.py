# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import string
from pathlib import Path

from drenv import kubectl

PACKAGE_DIR = Path(__file__).parent

STORAGE_CLASS_NAME_PREFIX = "rook-cephfs-"
FILE_SYSTEMS = ["fs1", "fs2"]


def start(cluster):
    """
    Deploy CephFS file systems, storage classes, and snapshot class, and
    wait until the file systems are ready.
    """
    deploy(cluster)
    wait(cluster)


def deploy(cluster):
    for file_system in FILE_SYSTEMS:
        print("Creating CephFS instance")
        template = _template("filesystem.yaml")
        yaml_str = template.substitute(cluster=cluster, name=file_system)
        kubectl.apply("--filename=-", input=yaml_str, context=cluster)

        print("Creating StorageClass")
        template = _template("storage-class.yaml")
        storage_class_name = STORAGE_CLASS_NAME_PREFIX + file_system
        yaml_str = template.substitute(
            cluster=cluster, fsname=file_system, name=storage_class_name
        )
        kubectl.apply("--filename=-", input=yaml_str, context=cluster)

    print("Creating SnapshotClass")
    template = _template("snapshot-class.yaml")
    storage_class_name = STORAGE_CLASS_NAME_PREFIX + FILE_SYSTEMS[0]
    yaml_str = template.substitute(cluster=cluster, scname=storage_class_name)
    kubectl.apply("--filename=-", input=yaml_str, context=cluster)


def wait(cluster):
    print("Waiting until Ceph File Systems are ready")

    for file_system in FILE_SYSTEMS:
        print(f"Waiting until cephfilesystem 'rook-ceph/{file_system}' exists")
        kubectl.wait(
            f"cephfilesystem/{file_system}",
            "--for=create",
            "--namespace=rook-ceph",
            context=cluster,
        )
        print(f"Waiting until cephfilesystem 'rook-ceph/{file_system}' is ready")
        kubectl.wait(
            f"cephfilesystem/{file_system}",
            "--for=jsonpath={.status.phase}=Ready",
            "--namespace=rook-ceph",
            context=cluster,
        )


def _template(name):
    path = PACKAGE_DIR / "start-data" / name
    with open(path) as f:
        return string.Template(f.read())
