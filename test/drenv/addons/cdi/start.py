# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import cache as _cache
from drenv import kubectl

from .config import CR_CACHE_KEY, OPERATOR_CACHE_KEY, PACKAGE_DIR

NAMESPACE = "cdi"


def start(cluster):
    """
    Deploy the CDI operator and CR, and wait until ready.
    """
    deploy(cluster)
    wait(cluster)


def deploy(cluster):
    print("Deploying cdi operator")
    path = _cache.get(str(PACKAGE_DIR / "start-data" / "operator"), OPERATOR_CACHE_KEY)
    kubectl.apply("--filename", path, context=cluster)

    print("Waiting until cdi-operator is rolled out")
    kubectl.rollout(
        "status",
        "deploy/cdi-operator",
        f"--namespace={NAMESPACE}",
        context=cluster,
    )

    print("Deploying cdi cr")
    path = _cache.get(str(PACKAGE_DIR / "start-data" / "cr"), CR_CACHE_KEY)
    kubectl.apply("--filename", path, context=cluster)


def wait(cluster):
    print("Waiting until cdi cr is available")
    kubectl.wait(
        "cdi.cdi.kubevirt.io/cdi",
        "--for=condition=available",
        f"--namespace={NAMESPACE}",
        context=cluster,
    )
    print("Waiting until cdi cr finished progressing")
    kubectl.wait(
        "cdi.cdi.kubevirt.io/cdi",
        "--for=condition=progressing=False",
        f"--namespace={NAMESPACE}",
        context=cluster,
    )
