# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import cache as _cache

from .config import CONTROLLER_CACHE_KEY, CRDS_CACHE_KEY, PACKAGE_DIR


def cache():
    """
    Refresh the cached kustomization yaml.
    """
    _cache.refresh(str(PACKAGE_DIR / "start-data" / "crds"), CRDS_CACHE_KEY)
    _cache.refresh(str(PACKAGE_DIR / "start-data" / "controller"), CONTROLLER_CACHE_KEY)
