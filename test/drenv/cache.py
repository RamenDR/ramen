# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import subprocess

from . import commands


def path(key):
    cache_home = os.environ.get("XDG_CACHE_HOME", ".cache")
    return os.path.expanduser(f"~/{cache_home}/drenv/{key}")


def fetch(kustomization_dir, dest, log=print):
    """
    Build kustomization and store the output yaml in dest.

    TODO: retry on errors.
    """
    if not os.path.exists(dest):
        log(f"Fetching {dest}")
        dest_dir = os.path.dirname(dest)
        os.makedirs(dest_dir, exist_ok=True)
        tmp = dest + f".tmp.{os.getpid()}"
        try:
            _build_kustomization(kustomization_dir, tmp)
            os.rename(tmp, dest)
        finally:
            _silent_remove(tmp)


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
