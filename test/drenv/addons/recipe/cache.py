# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import cache as _cache

from .config import CACHE_KEY, PACKAGE_DIR


def cache():
    _cache.refresh(str(PACKAGE_DIR / "start-data"), CACHE_KEY)
