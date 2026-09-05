# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
Tool management for drenv.

Kustomize is installed via a wrapper script in the venv bin directory that
invokes the version pinned in go.mod using `go tool kustomize`.
"""

import logging
import os
import sys

from packaging import version

from drenv import commands

REQUIRED_VERSION = "5.7.0"


def repo_root():
    """Return the ramen repository root directory."""
    return os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))


def in_venv():
    """Return True if running inside a virtual environment."""
    return sys.prefix != sys.base_prefix


def kustomize_version(path):
    """
    Return installed kustomize version at path or None if not installed.

    Args:
        path: Full path to kustomize binary or wrapper script.

    Returns:
        packaging.version.Version object or None
    """
    try:
        out = commands.run(path, "version")
    except commands.Error as e:
        if "No such file or directory" in str(e.error):
            return None
        raise RuntimeError(f"Failed to get kustomize version: {e.error}") from e

    try:
        return version.parse(out.strip().lstrip("v"))
    except Exception as e:
        raise RuntimeError(
            f"Failed to parse kustomize version from output '{out}': {e}"
        ) from e


def go_tool_kustomize_version():
    """Return kustomize version provided by go tool."""
    out = commands.run("go", "tool", "kustomize", "version", cwd=repo_root())
    return version.parse(out.strip().lstrip("v"))


def install_kustomize(install_dir):
    """Install kustomize wrapper script in install_dir."""
    os.makedirs(install_dir, exist_ok=True)
    path = os.path.join(install_dir, "kustomize")
    root = repo_root()
    with open(path, "w", encoding="utf-8") as f:
        f.write(f"""#!/bin/sh
cd {root!r} || exit 1
exec go tool kustomize "$@"
""")
    os.chmod(path, 0o755)


def setup(install_dir=None):
    """
    Check and install kustomize in the venv bin directory.

    Args:
        install_dir: Directory to install kustomize (default: venv bin).
                     Provided for testing.
    """
    if not in_venv():
        raise RuntimeError(
            "drenv tools must be installed in the ramen virtual environment"
        )

    if install_dir is None:
        install_dir = os.path.join(sys.prefix, "bin")

    required = version.parse(REQUIRED_VERSION)
    kustomize_path = os.path.join(install_dir, "kustomize")
    current = kustomize_version(kustomize_path)

    if current == required:
        logging.info("Using kustomize %s", current)
        return

    go_version = go_tool_kustomize_version()
    if go_version != required:
        raise RuntimeError(
            f"go tool kustomize version {go_version} does not match "
            f"required version {REQUIRED_VERSION}; update go.mod tool directive"
        )

    if current is None:
        logging.info("Installing kustomize %s", REQUIRED_VERSION)
    elif current < required:
        logging.info("Upgraded kustomize from %s to %s", current, REQUIRED_VERSION)
    else:
        logging.info("Downgraded kustomize from %s to %s", current, REQUIRED_VERSION)

    install_kustomize(install_dir)
