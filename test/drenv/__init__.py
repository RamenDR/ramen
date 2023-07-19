# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import string
import tempfile
import time

from contextlib import contextmanager

from . import kubectl


def wait_for(
    resource,
    output="jsonpath={.metadata.name}",
    timeout=300,
    namespace=None,
    profile=None,
    log=print,
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

    start = time.monotonic()
    deadline = start + timeout
    delay = min(0.1, timeout / 60)

    while True:
        out = kubectl.get(*args, context=profile)
        if out:
            elapsed = time.monotonic() - start
            log(f"{resource} outuput={output} found in {elapsed:.2f} seconds")
            return out

        if time.monotonic() > deadline:
            raise RuntimeError(f"Timeout waiting for {resource}")

        time.sleep(delay)


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
    share configuration between addons.
    """
    path = os.path.join("~", ".config", "drenv", name)
    return os.path.expanduser(path)
