# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import cache as _cache

from .config import CRDS_CACHE_KEY, OPERATORS_CACHE_KEY, PACKAGE_DIR


def cache():
    _cache.refresh(str(PACKAGE_DIR / "start-data" / "crds"), CRDS_CACHE_KEY)
    _cache.refresh(str(PACKAGE_DIR / "start-data" / "operators"), OPERATORS_CACHE_KEY)
