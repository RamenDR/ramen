# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import shutil
import subprocess

from . import commands


def clear(key=""):
    """
    Clear cached key. If key is not set clear the entire cache.
    """
    cache_dir = _path(key)
    try:
        shutil.rmtree(cache_dir)
    except FileNotFoundError:
        pass


def fetch(kustomization_dir, key, log=print):
    """
    Build kustomization and cache the output yaml. Retrun the path to the
    cached yaml.
    """
    dest = _path(key)
    if not os.path.exists(dest):
        _fetch(kustomization_dir, dest, log=log)
    return dest


def _fetch(kustomization_dir, dest, log=print):
    log(f"Fetching {dest}")
    dest_dir = os.path.dirname(dest)
    os.makedirs(dest_dir, exist_ok=True)
    tmp = dest + f".tmp.{os.getpid()}"
    try:
        _build_kustomization(kustomization_dir, tmp)
        os.rename(tmp, dest)
    finally:
        _silent_remove(tmp)


def _path(key):
    cache_home = os.environ.get("XDG_CACHE_HOME", ".cache")
    return os.path.expanduser(f"~/{cache_home}/drenv/{key}")


def _build_kustomization(kustomization_dir, dest):
    with open(dest, "w") as f:
        args = ["kustomize", "build", kustomization_dir]
        try:
            cp = subprocess.run(
                args,
                stdout=f,
                stderr=subprocess.PIPE,
            )
        except OSError as e:
            os.unlink(dest)
            raise commands.Error(args, f"Could not execute: {e}").with_exception(e)

        if cp.returncode != 0:
            error = cp.stderr.decode(errors="replace")
            raise commands.Error(args, error, exitcode=cp.returncode)

        os.fsync(f.fileno())


def _silent_remove(path):
    try:
        os.remove(path)
    except FileNotFoundError:
        pass
