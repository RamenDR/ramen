# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from pathlib import Path

from drenv import cache as _cache
from drenv import kubectl

PACKAGE_DIR = Path(__file__).parent
CACHE_KEY = "addons/rook-operator-1.18-5.yaml"


def start(cluster):
    """
    Deploy the rook ceph operator and wait until it is ready.
    """
    print("Deploying rook ceph operator")
    path = _cache.get(str(PACKAGE_DIR), CACHE_KEY)
    kubectl.apply("--filename", path, context=cluster)

    print("Waiting until rook ceph operator is rolled out")
    kubectl.rollout(
        "status",
        "deploy/rook-ceph-operator",
        "--namespace=rook-ceph",
        # We had random timeout with 300s.
        timeout=600,
        context=cluster,
    )

    print("Waiting until rook ceph operator is ready")
    kubectl.wait(
        "pod",
        "--for=jsonpath={.status.phase}=Running",
        "--namespace=rook-ceph",
        "--selector=app=rook-ceph-operator",
        context=cluster,
    )


def cache():
    """
    Refresh the cached kustomization yaml.
    """
    _cache.refresh(str(PACKAGE_DIR), CACHE_KEY)
