# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import cache as _cache
from drenv import kubectl

from .config import CONTROLLER_CACHE_KEY, CRDS_CACHE_KEY, PACKAGE_DIR


def start(cluster):
    """
    Deploy external-snapshotter CRDs and controller, and wait until ready.
    """
    deploy(cluster)
    wait(cluster)


def deploy(cluster):
    print("Deploying crds")
    path = _cache.get(str(PACKAGE_DIR / "start-data" / "crds"), CRDS_CACHE_KEY)
    kubectl.apply("--filename", path, context=cluster)

    print("Waiting until crds are established")
    kubectl.wait("--for=condition=established", "--filename", path, context=cluster)

    print("Deploying snapshot-controller")
    path = _cache.get(
        str(PACKAGE_DIR / "start-data" / "controller"), CONTROLLER_CACHE_KEY
    )
    kubectl.apply("--filename", path, context=cluster)


def wait(cluster):
    print("Waiting until snapshot-controller is rolled out")
    kubectl.rollout(
        "status",
        "deploy/snapshot-controller",
        "--namespace=kube-system",
        context=cluster,
    )
