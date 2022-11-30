# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import io
import pytest
from . import envfile

valid_yaml = """
name: test

templates:
  - name: dr-cluster
    memory: 6g
    network: default
    workers:
      # An unnamed worker
      - scripts:
          # Script accepting single arguemnt, the profile name
          - file: script1
          # Script with user set arguments, $name replaced by current profile
          # name.
          - file: script2
            args: ["$name", "hub"]
      # A named worker
      - name: named-worker
        scripts:
          - file: script3
  - name: hub-cluster
    memory: 4g
    network: default
    workers:
      - scripts:
          # Script that does not need its profile name.
          - file: script4
            args: ["dr1", "dr2"]

profiles:
  - name: dr1
    template: dr-cluster
    # Override template setting.
    memory: 8g
  - name: dr2
    template: dr-cluster
  - name: hub
    template: hub-cluster

workers:
  - scripts:
      # Script accepting third argument which is not a cluster name.
      - file: script5
        args: ["dr1", "dr2", "other"]
  - scripts:
      # Script accepting no arguments
      - file: script6
        args: []
"""


def test_valid():
    f = io.StringIO(valid_yaml)
    env = envfile.load(f)

    # profile dr1

    profile = env["profiles"][0]
    assert profile["name"] == "dr1"
    assert profile["network"] == "default"   # From template
    assert profile["memory"] == "8g"  # From profile
    assert profile["cpus"] == 2  # From defaults

    worker = profile["workers"][0]
    assert worker["name"] == "dr1/0"
    assert worker["scripts"][0]["args"] == ["dr1"]
    assert worker["scripts"][1]["args"] == ["dr1", "hub"]

    worker = profile["workers"][1]
    # XXX a bug, should be dr1/named-worker
    assert worker["name"] == "named-worker"

    # profile dr2

    profile = env["profiles"][1]
    assert profile["name"] == "dr2"
    assert profile["memory"] == "6g"  # From template

    worker = profile["workers"][0]
    assert worker["name"] == "dr2/0"
    assert worker["scripts"][0]["args"] == ["dr2"]
    assert worker["scripts"][1]["args"] == ["dr2", "hub"]

    worker = profile["workers"][1]
    # XXX should be dr2/named-worker
    assert worker["name"] == "named-worker"

    # profile hub

    profile = env["profiles"][2]
    assert profile["name"] == "hub"
    assert profile["memory"] == "4g"  # From template

    worker = profile["workers"][0]
    assert worker["name"] == "hub/0"
    assert worker["scripts"][0]["args"] == ["dr1", "dr2"]

    # env workers

    worker = env["workers"][0]
    assert worker["name"] == "test/0"
    assert worker["scripts"][0]["args"] == ["dr1", "dr2", "other"]

    worker = env["workers"][1]
    assert worker["name"] == "test/1"
    assert worker["scripts"][0]["args"] == []


def test_require_env_name():
    s = """
profiles: []
"""
    with pytest.raises(ValueError):
        envfile.load(io.StringIO(s))


def test_require_profiles():
    s = """
name: test
"""
    with pytest.raises(ValueError):
        envfile.load(io.StringIO(s))


def test_require_template_name():
    s = """
name: test
templates:
  - memory: 6g
profiles: []
"""
    with pytest.raises(ValueError):
        envfile.load(io.StringIO(s))


def test_require_profile_name():
    s = """
name: test
profiles:
  - memory: 6g
"""
    with pytest.raises(ValueError):
        envfile.load(io.StringIO(s))


def test_require_existing_template():
    s = """
name: test
profiles:
  - memory: 6g
    template: no-such-template
"""
    with pytest.raises(ValueError):
        envfile.load(io.StringIO(s))


def test_require_profile_script_file():
    s = """
name: test
profiles:
  - name: p1
    workers:
      - scripts:
          - args: ["arg1"]
"""
    with pytest.raises(ValueError):
        envfile.load(io.StringIO(s))


def test_require_env_script_file():
    s = """
name: test
profiles:
  - name: p1
workers:
  - scripts:
      - args: ["arg1"]
"""
    with pytest.raises(ValueError):
        envfile.load(io.StringIO(s))
