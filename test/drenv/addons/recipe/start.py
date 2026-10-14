# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import cache as _cache
from drenv import kubectl

from .config import CACHE_KEY, PACKAGE_DIR


def start(cluster):
    print("Deploying recipe crd")
    path = _cache.get(str(PACKAGE_DIR / "start-data"), CACHE_KEY)
    kubectl.apply("--filename", path, context=cluster)
