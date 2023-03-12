# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from . import commands


def config(*args, context=None):
    """
    Run kubectl config ... and return the output.
    """
    return _run("config", *args, context=context)


def get(*args, context=None):
    """
    Run kubectl get ... and return the output.
    """
    return _run("get", *args, context=context)


def exec(*args, context=None):
    """
    Run kubectl get ... and return the output.
    """
    return _run("exec", *args, context=context)


def apply(*args, input=None, context=None):
    """
    Run kubectl apply ... logging progress messages.
    """
    _watch("apply", *args, input=input, context=context)


def patch(*args, context=None):
    """
    Run kubectl patch ... logging progress messages.
    """
    _watch("patch", *args, context=context)


def label(name, value, overwrite=False, context=None):
    """
    Run kubectl label ... logging progress messages.
    """
    args = ["label", name, value]
    if overwrite:
        args.append("--overwrite")
    _watch(*args, context=context)


def delete(*args, input=None, context=None):
    """
    Run kubectl delete ... logging progress messages.
    """
    _watch("delete", *args, input=input, context=context)


def rollout(*args, context=None):
    """
    Run kubectl rollout ... logging progress messages.
    """
    _watch("rollout", *args, context=context)


def wait(*args, context=None):
    """
    Run kubectl wait ... logging progress messages.
    """
    _watch("wait", *args, context=context)


def _run(cmd, *args, context=None):
    cmd = ["kubectl", cmd]
    if context:
        cmd.extend(("--context", context))
    cmd.extend(args)
    return commands.run(*cmd)


def _watch(cmd, *args, input=None, context=None):
    cmd = ["kubectl", cmd]
    if context:
        cmd.extend(("--context", context))
    cmd.extend(args)
    for line in commands.watch(*cmd, input=input):
        print(line)
