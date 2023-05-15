# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from . import commands


def version(context=None, output=None):
    """
    Return local and server version info. Useful for testing connectivity to
    APIServer.
    """
    args = ["--output", output] if output else []
    return _run("version", *args, context=context)


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


def apply(*args, input=None, context=None, log=print):
    """
    Run kubectl apply ... logging progress messages.
    """
    _watch("apply", *args, input=input, context=context, log=log)


def patch(*args, context=None, log=print):
    """
    Run kubectl patch ... logging progress messages.
    """
    _watch("patch", *args, context=context, log=log)


def label(name, value, overwrite=False, context=None, log=print):
    """
    Run kubectl label ... logging progress messages.
    """
    args = ["label", name, value]
    if overwrite:
        args.append("--overwrite")
    _watch(*args, context=context, log=log)


def delete(*args, input=None, context=None, log=print):
    """
    Run kubectl delete ... logging progress messages.
    """
    _watch("delete", *args, input=input, context=context, log=log)


def rollout(*args, context=None, log=print):
    """
    Run kubectl rollout ... logging progress messages.
    """
    _watch("rollout", *args, context=context, log=log)


def wait(*args, context=None, log=print):
    """
    Run kubectl wait ... logging progress messages.
    """
    _watch("wait", *args, context=context, log=log)


def _run(cmd, *args, context=None):
    cmd = ["kubectl", cmd]
    if context:
        cmd.extend(("--context", context))
    cmd.extend(args)
    return commands.run(*cmd)


def _watch(cmd, *args, input=None, context=None, log=print):
    cmd = ["kubectl", cmd]
    if context:
        cmd.extend(("--context", context))
    cmd.extend(args)
    for line in commands.watch(*cmd, input=input):
        log(line)
