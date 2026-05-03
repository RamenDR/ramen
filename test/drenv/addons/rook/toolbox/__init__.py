# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from pathlib import Path

from drenv import cache as _cache
from drenv import ceph
from drenv import kubectl

PACKAGE_DIR = Path(__file__).parent
CACHE_KEY = "addons/rook-toolbox-1.18.yaml"


def start(cluster):
    """
    Deploy the rook ceph toolbox and wait until it is ready.
    """
    print("Deploying rook ceph toolbox")
    path = _cache.get(str(PACKAGE_DIR), CACHE_KEY)
    kubectl.apply("--filename", path, context=cluster)

    print("Waiting until rook-ceph-tools is rolled out")
    kubectl.rollout(
        "status",
        "deploy/rook-ceph-tools",
        "--namespace=rook-ceph",
        context=cluster,
    )

    print("ceph status:")
    print(ceph.tool(cluster, "ceph", "status").rstrip())


def cache():
    """
    Refresh the cached kustomization yaml.
    """
    _cache.refresh(str(PACKAGE_DIR), CACHE_KEY)
