# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import cache as _cache
from drenv import kubectl

from .config import CR_CACHE_KEY, OPERATOR_CACHE_KEY, PACKAGE_DIR

NAMESPACE = "kubevirt"


def start(cluster):
    """
    Deploy the kubevirt operator and CR, and wait until ready.
    """
    deploy(cluster)
    wait(cluster)


def deploy(cluster):
    print("Deploying kubevirt operator")
    path = _cache.get(str(PACKAGE_DIR / "start-data" / "operator"), OPERATOR_CACHE_KEY)
    kubectl.apply("--filename", path, context=cluster)

    print("Waiting until virt-operator is rolled out")
    kubectl.rollout(
        "status",
        "deploy/virt-operator",
        f"--namespace={NAMESPACE}",
        context=cluster,
    )

    print("Deploying kubevirt cr")
    path = _cache.get(str(PACKAGE_DIR / "start-data" / "cr"), CR_CACHE_KEY)
    kubectl.apply("--filename", path, context=cluster)


def wait(cluster):
    print("Waiting until kubevirt cr is available")
    kubectl.wait(
        "kubevirt.kubevirt.io/kubevirt",
        "--for=condition=available",
        f"--namespace={NAMESPACE}",
        context=cluster,
    )
