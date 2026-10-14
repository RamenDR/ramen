# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import string
import tempfile

from contextlib import contextmanager

from . import kubectl


def template(path):
    """
    Return a string.Template with contents of path.
    """
    with open(path) as f:
        return string.Template(f.read())


def kustomization(path, **kw):
    """
    Create a temporary kustomization directory using template at path,
    substituting values from kw.

    Returns a context manager to use with `kubectl -k`.
    """
    yaml_template = template(path)
    yaml = yaml_template.substitute(**kw)
    return kustomization_yaml(yaml)


@contextmanager
def kustomization_yaml(yaml):
    """
    Create a temporary kustomization directory using given yaml.

    Yields the directory path to be used with `kubectl -k`.
    """
    with tempfile.TemporaryDirectory(prefix="drenv") as tmpdir:
        path = os.path.join(tmpdir, "kustomization.yaml")
        with open(path, "w") as f:
            f.write(yaml)

        yield tmpdir


@contextmanager
def temporary_kubeconfig(prefix="drenv."):
    """
    Create a temporary kubeconfig and return an environment with KUBECONFIG
    pointing to the temporary kubeconfig. The environment can be used to run
    commands that do unsafe global modifications (use-context, set-context).

    The temporary kubeconfig is deleted when existing from the context.

    Yields the environment dict.
    """
    with tempfile.TemporaryDirectory(prefix=prefix) as tmpdir:
        kubeconfig = os.path.join(tmpdir, "kubeconfig")
        out = kubectl.config("view", "--flatten", "--output=yaml")
        with open(kubeconfig, "w") as f:
            f.write(out)
        env = dict(os.environ)
        env["KUBECONFIG"] = kubeconfig
        yield env


def config_dir(name):
    """
    Return configuration directory for profile name. This can be used to
    share configuration between addons.
    """
    path = os.path.join("~", ".config", "drenv", name)
    return os.path.expanduser(path)
