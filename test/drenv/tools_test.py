# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os

import pytest
from packaging import version

from drenv import commands
from drenv import tools


@pytest.fixture
def install_dir(tmp_path):
    return str(tmp_path / "bin")


def _fake_kustomize(path, ver):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(f"""#!/bin/sh
echo v{ver}
""")
    os.chmod(path, 0o755)


@pytest.fixture(autouse=True)
def require_venv(monkeypatch):
    monkeypatch.setattr(tools, "in_venv", lambda: True)


def test_setup_installs_required_version(install_dir):
    tools.setup(install_dir=install_dir)

    path = os.path.join(install_dir, "kustomize")
    assert os.path.isfile(path)
    assert tools.kustomize_version(path) == version.parse(tools.REQUIRED_VERSION)


def test_setup_upgrades_older_version(install_dir):
    path = os.path.join(install_dir, "kustomize")
    _fake_kustomize(path, "5.3.0")
    assert tools.kustomize_version(path) == version.parse("5.3.0")

    tools.setup(install_dir=install_dir)

    assert tools.kustomize_version(path) == version.parse(tools.REQUIRED_VERSION)


def test_setup_downgrades_newer_version(install_dir):
    path = os.path.join(install_dir, "kustomize")
    _fake_kustomize(path, "5.8.1")
    assert tools.kustomize_version(path) == version.parse("5.8.1")

    tools.setup(install_dir=install_dir)

    assert tools.kustomize_version(path) == version.parse(tools.REQUIRED_VERSION)


def test_setup_requires_venv(install_dir, monkeypatch):
    monkeypatch.setattr(tools, "in_venv", lambda: False)

    with pytest.raises(RuntimeError, match="virtual environment"):
        tools.setup(install_dir=install_dir)


def test_go_tool_kustomize_version():
    assert tools.go_tool_kustomize_version() == version.parse(tools.REQUIRED_VERSION)


def test_kustomize_wrapper_uses_go_tool(install_dir):
    tools.setup(install_dir=install_dir)

    path = os.path.join(install_dir, "kustomize")
    out = commands.run(path, "version")
    assert out.strip().lstrip("v") == tools.REQUIRED_VERSION
