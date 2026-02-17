# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import pytest

from . import marker


@pytest.fixture
def config_home(tmp_path, monkeypatch):
    """
    Redirect drenv.config_dir to use a temporary directory so tests
    don't touch the real ~/.config/drenv.
    """
    monkeypatch.setenv("HOME", str(tmp_path))
    return tmp_path


def test_exists_missing(config_home):
    assert not marker.exists("ex1/0", "example", "start")


def test_create_and_exists(config_home):
    marker.create("ex1/0", "example", "start")
    assert marker.exists("ex1/0", "example", "start")


def test_different_hooks_are_independent(config_home):
    marker.create("ex1/0", "example", "start")
    assert not marker.exists("ex1/0", "example", "test")


def test_different_addons_are_independent(config_home):
    marker.create("ex1/0", "minio", "start")
    assert not marker.exists("ex1/0", "example", "start")


def test_different_workers_are_independent(config_home):
    marker.create("ex1/0", "example", "start")
    assert not marker.exists("ex1/1", "example", "start")


def test_different_profiles_are_independent(config_home):
    marker.create("ex1/0", "example", "start")
    assert not marker.exists("ex2/0", "example", "start")


def test_marker_file_is_empty(config_home):
    marker.create("ex1/0", "example", "start")
    path = marker._path("ex1/0", "example", "start")
    assert os.path.getsize(path) == 0


def test_create_is_idempotent(config_home):
    marker.create("ex1/0", "example", "start")
    marker.create("ex1/0", "example", "start")
    assert marker.exists("ex1/0", "example", "start")
