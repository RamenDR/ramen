# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
Tool management for drenv.

This module provides functionality to check and install all tools required
by drenv. Each tool implements version() and install() methods.

This is a minimal implementation starting with kustomize only, addressing
PR #2538 review comments. Other tools will be added incrementally.
"""

import logging
import os
import platform
import shutil
import sys
import tarfile
import tempfile
import urllib.request

from packaging import version

from drenv import commands

# Platform detection - computed once at module import time
# All tools need the same values, so we cache them as module constants
_os_name = platform.system().lower()
_machine = platform.machine().lower()

# Normalize architecture names
if _machine in ("x86_64", "amd64"):
    ARCH = "amd64"
elif _machine in ("aarch64", "arm64"):
    ARCH = "arm64"
else:
    ARCH = _machine

OS_NAME = _os_name


class Tool:
    """Base class for all tools."""

    # Class attributes - override in subclasses
    name = None
    required_version = None
    archive_binary_path = None  # Exact path of binary in archive (e.g., "kustomize")

    def __init__(self, install_dir=None):
        """
        Initialize tool.

        Args:
            install_dir: Directory to install tool (default: venv bin directory)
        """
        self._install_dir = install_dir

    def install_dir(self):
        """
        Return the directory where tools are installed.

        The venv can be at different locations, so we cannot use a hardcoded
        path. All tools are installed in the same location.
        """
        if self._install_dir:
            return self._install_dir
        return os.path.join(sys.prefix, "bin")

    def path(self):
        """Return full path to tool binary in the venv."""
        return os.path.join(self.install_dir(), self.name)

    def version(self):
        """
        Return installed version or None if not installed.

        Must be implemented by subclass.

        Returns:
            packaging.version.Version object or None
        """
        raise NotImplementedError(
            f"{self.__class__.__name__}.version() not implemented"
        )

    def install(self):
        """
        Install the tool.

        Must be implemented by subclass.
        """
        raise NotImplementedError(
            f"{self.__class__.__name__}.install() not implemented"
        )

    def verify(self):
        """
        Check if tool is installed with correct version.

        Returns:
            True if tool is installed and version >= required_version
        """
        current = self.version()
        if current is None:
            return False
        return current >= version.parse(self.required_version)


# Helper functions


def download(url, target_path):
    """
    Download file from URL to target path.

    Args:
        url: URL to download from
        target_path: Local file path to save to
    """
    logging.debug(f"Downloading {url} to {target_path}")
    with urllib.request.urlopen(url) as response:
        with open(target_path, "wb") as f:
            shutil.copyfileobj(response, f)


def extract_tar(archive_path, target_dir, archive_binary_path):
    """
    Extract binary from tar archive.

    Args:
        archive_path: Path to tar.gz file
        target_dir: Directory to extract to
        archive_binary_path: Exact path of binary in the archive
    """
    logging.debug(f"Extracting {archive_binary_path} from {archive_path}")
    with tarfile.open(archive_path, "r") as tar:
        member = tar.getmember(archive_binary_path)
        # Extract binary to target_dir root, removing any directory structure from archive
        # This ensures the binary is placed directly in install_dir, not in a subdirectory
        member.name = os.path.basename(member.name)
        tar.extract(member, target_dir)


# Tool implementations


class Kustomize(Tool):
    """Kubernetes native configuration management."""

    # Version from drenv requirements
    name = "kustomize"
    required_version = "5.7.0"
    archive_binary_path = "kustomize"  # Binary is at root of archive

    # Download URL as class variable - resolved at import time
    download_url = (
        f"https://github.com/kubernetes-sigs/kustomize/"
        f"releases/download/kustomize%2Fv{required_version}/"
        f"kustomize_v{required_version}_{OS_NAME}_{ARCH}.tar.gz"
    )

    def version(self):
        """Return installed version or None if not installed."""
        try:
            out = commands.run(self.path(), "version")
        except commands.Error as e:
            if "No such file or directory" in str(e.error):
                return None
            # Provide context for other errors
            raise RuntimeError(f"Failed to get {self.name} version: {e.error}") from e

        try:
            version_str = out.strip().lstrip("v")
            return version.parse(version_str)
        except Exception as e:
            raise RuntimeError(
                f"Failed to parse {self.name} version from output '{out}': {e}"
            ) from e

    def install(self):
        """Install kustomize from GitHub releases."""
        with tempfile.TemporaryDirectory() as tmpdir:
            archive_path = os.path.join(tmpdir, "kustomize.tar.gz")
            download(self.download_url, archive_path)
            extract_tar(archive_path, self.install_dir(), self.archive_binary_path)
            os.chmod(self.path(), 0o755)


# Setup function


def setup(install_dir=None):
    """
    Check and install all required tools.

    Args:
        install_dir: Directory to install tools (default: venv bin directory)
                    This parameter is provided for testing purposes.
    """
    # List of all tools to install
    tools = [
        Kustomize(install_dir=install_dir),
    ]

    for tool in tools:
        current = tool.version()

        if current is None:
            logging.info(f"Installing {tool.name} {tool.required_version}")
            try:
                tool.install()
            except Exception as e:
                logging.error(f"Failed to install {tool.name}: {e}")
                raise
        elif current < version.parse(tool.required_version):
            logging.info(
                f"Updating {tool.name} from {current} to {tool.required_version}"
            )
            try:
                tool.install()
            except Exception as e:
                logging.error(f"Failed to update {tool.name}: {e}")
                raise
        else:
            logging.info(f"Using {tool.name} {current}")
