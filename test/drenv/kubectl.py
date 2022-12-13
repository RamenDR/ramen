# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import drenv
from . import commands


def config(*args, profile=None):
    """
    Run kubectl config ... and return the output.
    """
    return _run("config", *args, profile=profile)


def get(*args, profile=None):
    """
    Run kubectl get ... and return the output.
    """
    return _run("get", *args, profile=profile)


def exec(*args, profile=None):
    """
    Run kubectl get ... and return the output.
    """
    return _run("exec", *args, profile=profile)


def apply(*args, input=None, profile=None):
    """
    Run kubectl apply ... logging progress messages.
    """
    _watch("apply", *args, input=input, profile=profile)


def patch(*args, profile=None):
    """
    Run kubectl patch ... logging progress messages.
    """
    _watch("patch", *args, profile=profile)


def delete(*args, input=None, profile=None):
    """
    Run kubectl delete ... logging progress messages.
    """
    _watch("delete", *args, input=input, profile=profile)


def rollout(*args, profile=None):
    """
    Run kubectl rollout ... logging progress messages.
    """
    _watch("rollout", *args, profile=profile)


def wait(*args, profile=None):
    """
    Run kubectl wait ... logging progress messages.
    """
    _watch("wait", *args, profile=profile)


def _run(cmd, *args, profile=None):
    cmd = ["kubectl", cmd]
    if profile:
        cmd.extend(("--context", profile))
    cmd.extend(args)
    return commands.run(*cmd)


def _watch(cmd, *args, input=None, profile=None):
    cmd = ["kubectl", cmd]
    if profile:
        cmd.extend(("--context", profile))
    cmd.extend(args)
    for line in commands.watch(*cmd, input=input):
        drenv.log_detail(line)
