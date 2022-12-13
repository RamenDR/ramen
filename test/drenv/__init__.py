# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import json
import os
import string
import subprocess
import tempfile
import textwrap
import time

from contextlib import contextmanager

from . import commands


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


def kubectl_get(*args, profile=None):
    """
    Run kubectl get ... and return the output.
    """
    return _kubectl_run("get", *args, profile=profile)


def kubectl_exec(*args, profile=None):
    """
    Run kubectl get ... and return the output.
    """
    return _kubectl_run("exec", *args, profile=profile)


def kubectl_apply(*args, input=None, profile=None):
    """
    Run kubectl apply ... logging progress messages.
    """
    _kubectl_watch("apply", *args, input=input, profile=profile)


def kubectl_patch(*args, profile=None):
    """
    Run kubectl patch ... logging progress messages.
    """
    _kubectl_watch("patch", *args, profile=profile)


def kubectl_delete(*args, input=None, profile=None):
    """
    Run kubectl delete ... logging progress messages.
    """
    _kubectl_watch("delete", *args, input=input, profile=profile)


def kubectl_rollout(*args, profile=None):
    """
    Run kubectl rollout ... logging progress messages.
    """
    _kubectl_watch("rollout", *args, profile=profile)


def kubectl_wait(*args, profile=None):
    """
    Run kubectl wait ... logging progress messages.
    """
    _kubectl_watch("wait", *args, profile=profile)


def _kubectl_run(cmd, *args, profile=None):
    cmd = ["kubectl", cmd]
    if profile:
        cmd.extend(("--context", profile))
    cmd.extend(args)
    return commands.run(*cmd)


def _kubectl_watch(cmd, *args, input=None, profile=None):
    cmd = ["kubectl", cmd]
    if profile:
        cmd.extend(("--context", profile))
    cmd.extend(args)
    for line in commands.watch(*cmd, input=input):
        log_detail(line)


def kubectl(*args, profile=None, input=None, verbose=True):
    """
    Run `kubectl` command for profile.

    To pipe yaml into the kubectl command, use `--filename -` and pass the yaml
    to the input argument.

    The underlying kubectl command output is logged using log_detail(). Set
    verbose=False the log.

    Returns the underlying command output.
    """
    cmd = ["kubectl"]
    if profile:
        cmd.extend(("--context", profile))
    cmd.extend(args)

    return run(*cmd, input=input, verbose=verbose)


def wait_for(
    resource,
    output="jsonpath={.metadata.name}",
    timeout=300,
    namespace=None,
    profile=None,
):
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
    args = [resource, "--output", output, "--ignore-not-found"]
    if namespace:
        args.extend(("--namespace", namespace))

    deadline = time.monotonic() + timeout
    delay = min(1.0, timeout / 60)

    while True:
        out = kubectl_get(*args, profile=profile)
        if out:
            log_detail(f"{resource} exists")
            return out

        if time.monotonic() > deadline:
            raise RuntimeError(f"Timeout waiting for {resource}")

        time.sleep(delay)


def wait_for_cluster(cluster, timeout=300):
    """
    Wait until a cluster is available.

    This is useful when starting profiles concurrently, when one profile needs
    to wait for another profile.
    """
    deadline = time.monotonic() + timeout
    delay = min(1.0, timeout / 60)
    last_status = None

    while True:
        status = cluster_status(cluster)
        current_status = status.get("APIServer", "Unknown")

        if current_status != last_status:
            log_detail(f"cluster {cluster} status is {current_status}")
            last_status = current_status

        if current_status == "Running":
            break

        if time.monotonic() > deadline:
            raise RuntimeError(f"Timeout waiting for {cluster}")

        time.sleep(delay)


def cluster_status(cluster):
    """
    Return minikube status for cluster or empty dict if the cluster does not
    exist or not configured with kubectl yet.
    """
    # To avoid lot of noise in the logs, fetch status only if kubectl knows
    # about this cluster.
    if not cluster_info(cluster):
        return {}

    out = run(
        "minikube",
        "status",
        "--profile",
        cluster,
        "--output",
        "json",
        verbose=False,
    )

    return json.loads(out)


def cluster_exists(cluster):
    out = run("minikube", "profile", "list", "--output=json", verbose=False)
    profiles = json.loads(out)
    for profile in profiles["valid"]:
        if profile["Name"] == cluster:
            return True

    return False


def cluster_info(cluster):
    """
    Return cluster info from kubectl config. Returns empty dict if the cluster
    is not configured with kubectl yet.
    """
    out = kubectl("config", "view", "--output", "json", verbose=False)
    config = json.loads(out)

    # We get null instead of [].
    clusters = config.get("clusters") or ()

    for c in clusters:
        if c["name"] == cluster:
            return c

    return {}


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
        check=True,
    )

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
