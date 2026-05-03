# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import cache as _cache

from .config import CR_CACHE_KEY, OPERATOR_CACHE_KEY, PACKAGE_DIR


def cache():
    """
    Refresh the cached kustomization yaml.
    """
    _cache.refresh(str(PACKAGE_DIR / "start-data" / "operator"), OPERATOR_CACHE_KEY)
    _cache.refresh(str(PACKAGE_DIR / "start-data" / "cr"), CR_CACHE_KEY)
