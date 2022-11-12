# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import string
import subprocess
import tempfile
import textwrap
import time

from contextlib import contextmanager


def log_progress(msg):
    """
    Logs progress mesage to stdout.
    """
    print(f"* {msg}")


def log_detail(text):
    """
    Logs details for the last progress message to stdout.
    """
    print(textwrap.indent(text, "  "))


def kubectl(*args, profile=None, input=None, verbose=True):
    """
    Run `minikube kubectl` command for profile.

    Some kubectl commands (e.g. config) do not work with profile and require
    `--context profile` in the command arguments.

    To pipe yaml into the kubectl command, use `--filename -` and pass the yaml
    to the input argument.

    The underlying kubectl command output is logged using log_detail(). Set
    verbose=False the log.

    Returns the underlying command output.
    """
    cmd = ["minikube", "kubectl"]
    if profile:
        cmd.extend(("--profile", profile))
    cmd.append("--")
    cmd.extend(args)

    return run(*cmd, input=input, verbose=verbose)


def wait_for(resource, output="jsonpath={.metadata.name}", timeout=300,
             namespace=None, profile=None):
    """
    Wait until resource exists. Once the resource exists, wait for it
    using `kubectl wait`.

    To wait for a specific part of the resource specify a kubectl output
    specficiation (e.g. output="jsonpath={.status.phase}"). The function
    returns when the output is non empty.

    Returns the resource .metadata.name, or if output was specified, the
    specified outpout for the resource.

    Raises RuntimeError if the resource does not exist within the specified
    timeout.
    """
    args = ["get", resource, "--output", output, "--ignore-not-found"]
    if namespace:
        args.extend(("--namespace", namespace))

    deadline = time.monotonic() + timeout
    delay = min(1.0, timeout / 60)

    while True:
        out = kubectl(*args, profile=profile, verbose=False)
        if out:
            log_detail(f"{resource} exists")
            return out

        if time.monotonic() > deadline:
            raise RuntimeError(f"Timeout waiting for {resource}")

        time.sleep(delay)


def run(*args, input=None, verbose=True):
    """
    Run a command and return the output.

    You can set input to the text to pipe into the commnad stdin.

    The underlying command output is logged using log_detail(). Set
    verbose=False to suppress the log.
    """
    cp = subprocess.run(
        args,
        input=input.encode() if input else None,
        stdout=subprocess.PIPE,
        check=True)

    out = cp.stdout.decode().rstrip()

    # Log output for debugging so we don't need to log manually for every
    # command.
    if out and verbose:
        log_detail(out)

    return out


def template(path):
    """
    Retrun a string.Template with contents of path.
    """
    with open(path) as f:
        return string.Template(f.read())


@contextmanager
def kustomization(path, **kw):
    """
    Create a temporary kustomization directory using template at path,
    substituting values from kw.

    Yields the directory path to be used with `kubectl -k`.
    """
    yaml_template = template(path)
    yaml = yaml_template.substitute(**kw)

    with tempfile.TemporaryDirectory(prefix="drenv") as tmpdir:
        kustomization_yaml = os.path.join(tmpdir, "kustomization.yaml")
        with open(kustomization_yaml, "w") as f:
            f.write(yaml)

        yield tmpdir


def config_dir(name):
    """
    Return configuration directory for profile name. This can be used to
    share configuration between scripts.
    """
    path = os.path.join("~", ".config", "drenv", name)
    return os.path.expanduser(path)
