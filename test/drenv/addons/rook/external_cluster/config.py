# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from pathlib import Path

VERSION = "v1.17.7"
BASE_URL = (
    f"https://raw.githubusercontent.com/rook/rook/refs/tags/{VERSION}/deploy/examples"
)
IMPORT_SCRIPT_URL = f"{BASE_URL}/import-external-cluster.sh"
CREATE_EXTERNAL_CLUSTER_RESOURCES_URL = (
    f"{BASE_URL}/create-external-cluster-resources.py"
)
CACHE_KEY = f"addons/rook-import-external-cluster-{VERSION}.sh"
ROOK_CEPH_NAMESPACE = "rook-ceph"

IMPORT_STORAGE_CLASS_NAMES = ["ceph-rbd", "ceph-rbd-topology", "cephfs"]

STORAGE_CLASSES = [
    {"name": "rook-ceph-block", "pool": "replicapool"},
    {"name": "rook-ceph-block-2", "pool": "replicapool-2"},
]

PACKAGE_DIR = Path(__file__).parent
