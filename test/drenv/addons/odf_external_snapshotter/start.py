# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import cache as _cache
from drenv import kubectl

from .config import CACHE_KEY, PACKAGE_DIR


def start(cluster):
    print("Deploying crds")
    path = _cache.get(str(PACKAGE_DIR / "start-data" / "crds"), CACHE_KEY)
    kubectl.apply("--filename", path, context=cluster)

    print("Waiting until crds are established")
    kubectl.wait("--for=condition=established", "--filename", path, context=cluster)
