# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import cache as _cache
from drenv import kubectl

from .config import CACHE_KEY, PACKAGE_DIR


def start(cluster):
    """
    Deploy csi-addons controller and wait until it is ready.
    """
    print("Deploying csi addon for volume replication")
    path = _cache.get(str(PACKAGE_DIR / "start-data"), CACHE_KEY)
    kubectl.apply("--filename", path, context=cluster)

    print(
        "Waiting until deployment 'csi-addons-system/csi-addons-controller-manager'"
        " is rolled out"
    )
    kubectl.rollout(
        "status",
        "deployment/csi-addons-controller-manager",
        "--namespace=csi-addons-system",
        context=cluster,
    )
