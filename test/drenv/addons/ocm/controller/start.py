# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import cache as _cache
from drenv import kubectl

from .config import CACHE_KEY, PACKAGE_DIR


def start(cluster):
    deploy(cluster)
    wait(cluster)


def deploy(cluster):
    print("Deploying ocm controller")
    path = _cache.get(str(PACKAGE_DIR / "start-data"), CACHE_KEY)
    kubectl.apply("--filename", path, context=cluster)


def wait(cluster):
    print("Waiting for ocm controller rollout")
    kubectl.rollout(
        "status",
        "deploy/ocm-controller",
        "--namespace=open-cluster-management",
        context=cluster,
    )
