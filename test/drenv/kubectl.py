# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from . import commands


def version(context=None, output=None):
    """
    Return local and server version info. Useful for testing connectivity to
    APIServer.
    """
    args = ["--output", output] if output else []
    try:
        return _run("version", *args, context=context)
    except commands.Error as e:
        # If kubectl provided output this is not really an error and the caller
        # can use the output.
        if e.output:
            return e.output
        raise


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


def describe(*args, context=None):
    return _run("describe", *args, context=context)


def exec(*args, context=None):
    """
    Run kubectl get ... and return the output.
    """
    return _run("exec", *args, context=context)


def events(resource, namespace=None, context=None):
    """
    Run kubectl events ... and return the output.
    """
    args = [resource]
    if namespace:
        args.append(f"--namespace={namespace}")
    return _run("events", *args, context=context)


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


def rollout(action, resource, timeout=300, namespace=None, context=None, log=print):
    """
    Run kubectl rollout ... logging progress messages.
    """
    args = [action, resource, f"--timeout={timeout}s"]
    if namespace:
        args.append(f"--namespace={namespace}")
    _watch("rollout", *args, context=context, log=log)


def wait(
    resource=None,
    all=False,
    selector=None,
    filename=None,
    condition=None,
    namespace=None,
    timeout=300,
    context=None,
    log=print,
):
    """
    Run kubectl wait ... logging progress messages.
    """
    args = [f"--timeout={timeout}s"]
    if resource:
        args.append(resource)
    if all:
        args.append("--all")
    if selector:
        args.append(f"--selector={selector}")
    if filename:
        args.append(f"--filename={filename}")
    if condition:
        args.append(f"--for={condition}")
    if namespace:
        args.append(f"--namespace={namespace}")
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
