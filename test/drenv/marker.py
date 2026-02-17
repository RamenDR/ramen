# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import logging
import os

import drenv


def exists(worker_name, addon_name, hook):
    path = _path(worker_name, addon_name, hook)
    return os.path.exists(path)


def create(worker_name, addon_name, hook):
    path = _path(worker_name, addon_name, hook)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        f.flush()
        os.fsync(f.fileno())
    logging.debug("[%s] Created marker %s", worker_name, path)


def _path(worker_name, addon_name, hook):
    """
    Marker path encodes (profile, worker_index, addon, hook).

    worker_name is already in the form "profile/index" (e.g. "ex1/0"),
    so splitting on "/" gives us the profile name and worker index which
    together with addon_name and hook produce a unique path under the
    profile's config directory.

    Example: worker_name="ex1/0", addon_name="example", hook="start"
    => ~/.config/drenv/ex1/completed/0/example.start
    """
    profile_name, worker_index = worker_name.rsplit("/", 1)
    filename = f"{addon_name}.{hook}"
    return os.path.join(
        drenv.config_dir(profile_name), "completed", worker_index, filename
    )
