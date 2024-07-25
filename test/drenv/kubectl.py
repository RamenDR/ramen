# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from . import commands

JSONPATH_NEWLINE = '{"\\n"}'


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


def create(*args, context=None):
    """
    Run kubectl create ... and return the output.
    """
    return _run("create", *args, context=context)


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


def label(resource, label, overwrite=False, context=None, log=print):
    """
    Run kubectl resource label ... logging progress messages.

    Set label="name=value" to set a label, label="name-" to remove a label.
    """
    args = ["label", resource, label]
    if overwrite:
        args.append("--overwrite")
    _watch(*args, context=context, log=log)


def annotate(
    resource,
    annotations,
    overwrite=False,
    namespace=None,
    context=None,
    log=print,
):
    """
    Run kubectl annotate ... logging progress messages.

    annotations is a dict of keys and values. Use key: None to remove an
    annotation.
    """
    args = ["annotate", resource]

    # Convert kubectl argument list:
    # {"add": "value", "remove": None} -> ["add=value", "remove-"]
    for key, value in annotations.items():
        if value:
            args.append(f"{key}={value}")
        else:
            args.append(f"{key}-")

    if overwrite:
        args.append("--overwrite")
    if namespace:
        args.extend(("--namespace", namespace))

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


def watch(
    resource,
    jsonpath="{}",
    namespace=None,
    timeout=None,
    context=None,
):
    """
    Run kubectl get --watch --output={jsonpath} ... iterating over lines from
    kubectl stdout.

    The resource argument may be kind ("pod") or a single resource
    ("pod/pod-name").

    Since watch waits for a complete line, a JSONPATH_NEWLINE is added to
    specified jsonpath argument unless the jsonpath already ends with one.

    Iteration stops when the timeout expires, or the underlying kubectl command
    terminated with a zero exit code.

    To end watching early, call close() on the return value.

    Raises:
    - commands.Error if starting kubectl failed.
    - comamnds.Timeout if timeout has expired.
    """
    if not jsonpath.endswith(JSONPATH_NEWLINE):
        jsonpath += JSONPATH_NEWLINE

    cmd = [
        "kubectl",
        "get",
        resource,
        "--watch",
        f"--output=jsonpath={jsonpath}",
    ]
    if namespace:
        cmd.append(f"--namespace={namespace}")
    if context:
        cmd.append(f"--context={context}")

    return commands.watch(*cmd, timeout=timeout)


def gather(contexts, namespaces=None, directory=None):
    """
    Run kubectl gather plugin.
    """
    cmd = ["kubectl", "gather", "--contexts", ",".join(contexts)]
    if namespaces:
        cmd.extend(("--namespaces", ",".join(namespaces)))
    if directory:
        cmd.extend(("--directory", directory))
    commands.run(*cmd)


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
