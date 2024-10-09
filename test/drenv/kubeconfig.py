# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import logging
import os
import tempfile
import time
from contextlib import contextmanager

from . import kubectl
from . import yaml

DEFAULT_CONFIG = os.path.expanduser("~/.kube/config")


def merge(profile, *sources, target=DEFAULT_CONFIG):
    """
    Merge one or more source kubeconfigs into target kubeconfig, adding or
    replacing items in the target.
    """
    logging.debug(
        "[%s] Merging kubeconfigs %s into %s",
        profile["name"],
        sources,
        target,
    )
    configs = [target]
    configs.extend(sources)
    env = dict(os.environ)
    env["KUBECONFIG"] = ":".join(configs)
    with _lockfile(profile, target):
        data = kubectl.config("view", "--flatten", env=env)
        _write(target, data)


def remove(profile, target=DEFAULT_CONFIG):
    """
    Remove context, cluster, and user from target. Assumes that all share the
    same name.

    kubectl config is not idempotent and fail when deleting non-existing items.
    It also does not provide a way to list clusters and users, so we cannot
    check if a user or cluster exists.
    """
    logging.debug("[%s] Removing cluster config from '%s'", profile["name"], target)
    modified = False
    with _lockfile(profile, target):
        try:
            config = _load(target)
        except FileNotFoundError:
            return

        for k in ("contexts", "clusters", "users"):
            old = config.get(k) or []
            new = [v for v in old if v["name"] != profile["name"]]
            if len(new) < len(old):
                config[k] = new
                modified = True

        if config["current-context"] == profile["name"]:
            config["current-context"] = ""
            modified = True

        if not modified:
            return

        data = yaml.dump(config)
        _write(target, data)


@contextmanager
def _lockfile(profile, target, attempts=10, delay=0.01, factor=2.0):
    """
    Lock file compatible with `kubectl config` or other go programs using the
    same library.
    https://github.com/kubernetes/client-go/blob/master/tools/clientcmd/loader.go

    This is a pretty bad way to lock files, but we don't have a choices since
    kubectl is using this method.

    In client-go there is no retry - if the lockfile exists, the command will
    fail:

        % kubectl config rename-context minikube foo
        error: open /Users/nsoffer/.kube/config.lock: file exists

    This is not usedul behavior for drenv since it leads to retrying of very
    slow operations, so we retry the operation with exponential backoff before
    failing.

    However if the lockfile was left over after killing kubectl or drenv, the
    only way to recover is to delete the lock file.
    """
    lockfile = target + ".lock"

    logging.debug("[%s] Creating lockfile '%s'", profile["name"], lockfile)
    os.makedirs(os.path.dirname(lockfile), exist_ok=True)
    for i in range(1, attempts + 1):
        try:
            fd = os.open(lockfile, os.O_CREAT | os.O_EXCL, 0)
        except FileExistsError:
            if i == attempts:
                raise
            time.sleep(delay)
            delay *= factor
        else:
            os.close(fd)
            break
    try:
        yield
    finally:
        logging.debug("[%s] Removing lockfile '%s'", profile["name"], lockfile)
        try:
            os.remove(lockfile)
        except FileNotFoundError:
            logging.warning(
                "[%s] Lockfile '%s' was removed while locked",
                profile["name"],
                lockfile,
            )


def _load(target):
    with open(target) as f:
        return yaml.safe_load(f)


def _write(target, data):
    fd, tmp = tempfile.mkstemp(
        dir=os.path.dirname(target),
        prefix=os.path.basename(target),
        suffix=".tmp",
    )
    try:
        os.write(fd, data.encode())
        os.fsync(fd)
        os.rename(tmp, target)
    except BaseException:
        os.remove(tmp)
        raise
    finally:
        os.close(fd)
