# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import io
import pytest
from collections import namedtuple

from . import envfile

Env = namedtuple("Env", "file,addons_root")


@pytest.fixture
def valid_env(tmpdir):
    yaml = """
name: test

ramen:
  hub: hub
  clusters: [dr1, dr2]
  topology: regional-dr

templates:
  - name: dr-cluster
    memory: 6g
    workers:
      # An unnamed worker
      - addons:
          # Addon accepting single arguemnt, the profile name
          - name: addon1
          # Addon with user set arguments, $name replaced by current profile
          # name.
          - name: addon2
            args: ["$name", "hub"]
      # A named worker
      - name: named-worker
        addons:
          - name: addon3
  - name: hub-cluster
    memory: 4g
    workers:
      - addons:
          # Addon that does not need its profile name.
          - name: addon4
            args: ["dr1", "dr2"]

profiles:
  - name: dr1
    template: dr-cluster
    # Override template setting.
    memory: 8g
  - name: dr2
    template: dr-cluster
    # These are specific values and will be propagated to minikube as is.
    driver: myhypervisor
    network: mynetwork
  - name: hub
    external: true
    template: hub-cluster
    # Using driver and network from platform defaults
    driver: $container
    network: $network
  - name: dr3
    template: dr-cluster
    # Using driver from platform defaults
    driver: $vm

workers:
  - name: named-worker
    addons:
      # Addon accepting third argument which is not a cluster name.
      - name: addon5
        args: ["dr1", "dr2", "other"]
  - addons:
      # Addon accepting no arguments
      - name: addon6
        args: []
"""

    for i in range(1, 7):
        tmpdir.mkdir(f"addon{i}")

    return Env(file=io.StringIO(yaml), addons_root=str(tmpdir))


def test_driver(valid_env):
    env = envfile.load(valid_env.file, addons_root=valid_env.addons_root)
    platform_defaults = envfile.platform_defaults()

    # no driver
    profile = env["profiles"][0]
    assert profile["driver"] == platform_defaults[envfile.VM]

    # concrete driver
    profile = env["profiles"][1]
    assert profile["driver"] == "myhypervisor"

    # platform container driver
    profile = env["profiles"][2]
    assert profile["driver"] == platform_defaults[envfile.CONTAINER]

    # platform vm driver
    profile = env["profiles"][3]
    assert profile["driver"] == platform_defaults[envfile.VM]


def test_network(valid_env):
    env = envfile.load(valid_env.file, addons_root=valid_env.addons_root)
    platform_defaults = envfile.platform_defaults()

    # no network
    profile = env["profiles"][0]
    assert profile["network"] == ""

    # concrete network
    profile = env["profiles"][1]
    assert profile["network"] == "mynetwork"

    # platform drenv-shared network
    profile = env["profiles"][2]
    assert profile["network"] == platform_defaults[envfile.SHARED_NETWORK]


def test_valid(valid_env):
    env = envfile.load(valid_env.file, addons_root=valid_env.addons_root)

    # profile dr1

    profile = env["profiles"][0]
    assert profile["name"] == "dr1"
    assert not profile["external"]
    assert profile["memory"] == "8g"  # From profile
    assert profile["cpus"] == 2  # From defaults

    worker = profile["workers"][0]
    assert worker["name"] == "dr1/0"
    assert worker["addons"][0]["args"] == ["dr1"]
    assert worker["addons"][1]["args"] == ["dr1", "hub"]

    worker = profile["workers"][1]
    assert worker["name"] == "dr1/named-worker"

    # profile dr2

    profile = env["profiles"][1]
    assert profile["name"] == "dr2"
    assert profile["memory"] == "6g"  # From template

    worker = profile["workers"][0]
    assert worker["name"] == "dr2/0"
    assert worker["addons"][0]["args"] == ["dr2"]
    assert worker["addons"][1]["args"] == ["dr2", "hub"]

    worker = profile["workers"][1]
    assert worker["name"] == "dr2/named-worker"

    # profile hub

    profile = env["profiles"][2]
    assert profile["name"] == "hub"
    assert profile["external"]
    assert profile["memory"] == "4g"  # From template

    worker = profile["workers"][0]
    assert worker["name"] == "hub/0"
    assert worker["addons"][0]["args"] == ["dr1", "dr2"]

    # env workers

    worker = env["workers"][0]
    assert worker["name"] == "test/named-worker"
    assert worker["addons"][0]["args"] == ["dr1", "dr2", "other"]

    worker = env["workers"][1]
    assert worker["name"] == "test/1"
    assert worker["addons"][0]["args"] == []


def test_name_prefix(valid_env):
    env = envfile.load(
        valid_env.file,
        name_prefix="prefix-",
        addons_root=valid_env.addons_root,
    )

    # env

    assert env["name"] == "prefix-test"

    # ramen info

    assert env["ramen"] == {
        "hub": "prefix-hub",
        "clusters": ["prefix-dr1", "prefix-dr2"],
        "topology": "regional-dr",
    }

    # profile dr1

    profile = env["profiles"][0]
    assert profile["name"] == "prefix-dr1"

    worker = profile["workers"][0]
    assert worker["name"] == "prefix-dr1/0"
    assert worker["addons"][0]["args"] == ["prefix-dr1"]
    assert worker["addons"][1]["args"] == ["prefix-dr1", "prefix-hub"]

    worker = profile["workers"][1]
    assert worker["name"] == "prefix-dr1/named-worker"

    # profile dr2

    profile = env["profiles"][1]
    assert profile["name"] == "prefix-dr2"

    worker = profile["workers"][0]
    assert worker["name"] == "prefix-dr2/0"
    assert worker["addons"][0]["args"] == ["prefix-dr2"]
    assert worker["addons"][1]["args"] == ["prefix-dr2", "prefix-hub"]

    worker = profile["workers"][1]
    assert worker["name"] == "prefix-dr2/named-worker"

    # profile hub

    profile = env["profiles"][2]
    assert profile["name"] == "prefix-hub"

    worker = profile["workers"][0]
    assert worker["name"] == "prefix-hub/0"
    assert worker["addons"][0]["args"] == ["prefix-dr1", "prefix-dr2"]

    # env workers

    worker = env["workers"][0]
    assert worker["name"] == "prefix-test/named-worker"
    assert worker["addons"][0]["args"] == [
        "prefix-dr1",
        "prefix-dr2",
        "other",
    ]

    worker = env["workers"][1]
    assert worker["name"] == "prefix-test/1"
    assert worker["addons"][0]["args"] == []


def test_missing_profile_addons(tmpdir):
    s = """
name: test
profiles:
  - name: dr1
    workers:
      - addons:
          - name: addon
"""
    with pytest.raises(envfile.MissingAddon):
        envfile.load(io.StringIO(s), addons_root=str(tmpdir))


def test_missing_global_addons(tmpdir):
    s = """
name: test
profiles:
  - name: dr1
workers:
  - addons:
      - name: missing
"""
    with pytest.raises(envfile.MissingAddon):
        envfile.load(io.StringIO(s), addons_root=str(tmpdir))


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


def test_require_profile_addon_name():
    s = """
name: test
profiles:
  - name: p1
    workers:
      - addons:
          - args: ["arg1"]
"""
    with pytest.raises(ValueError):
        envfile.load(io.StringIO(s))


def test_require_env_addon_name():
    s = """
name: test
profiles:
  - name: p1
workers:
  - addons:
      - args: ["arg1"]
"""
    with pytest.raises(ValueError):
        envfile.load(io.StringIO(s))
