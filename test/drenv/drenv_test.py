# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import json
import os

import yaml
import pytest

import drenv
from drenv import cluster
from drenv import commands
from drenv import kubectl

EXAMPLE_ENV = os.path.join("envs", "example.yaml")
EXTERNAL_ENV = os.path.join("envs", "external.yaml")


def test_start_unknown():
    # Cluster does not exists, so it should fail.
    with pytest.raises(commands.Error):
        commands.run("drenv", "start", "--name-prefix", "unknown-", EXTERNAL_ENV)


def test_start(tmpenv):
    commands.run("drenv", "start", "--name-prefix", tmpenv.prefix, EXTERNAL_ENV)
    assert cluster.status(tmpenv.prefix + "cluster") == cluster.READY


def test_dump_without_prefix():
    out = commands.run("drenv", "dump", EXAMPLE_ENV)
    dump = yaml.safe_load(out)
    assert dump["profiles"][0]["name"] == "ex1"
    assert dump["profiles"][1]["name"] == "ex2"


def test_dump_with_prefix():
    out = commands.run("drenv", "dump", "--name-prefix", "test-", EXAMPLE_ENV)
    dump = yaml.safe_load(out)
    assert dump["profiles"][0]["name"] == "test-ex1"
    assert dump["profiles"][1]["name"] == "test-ex2"


def test_stop_unknown():
    # Does nothing, so should succeed.
    commands.run("drenv", "stop", "--name-prefix", "unknown-", EXTERNAL_ENV)


def test_stop(tmpenv):
    # Stop does nothing, so cluster must be ready.
    commands.run("drenv", "stop", "--name-prefix", tmpenv.prefix, EXTERNAL_ENV)
    assert cluster.status(tmpenv.prefix + "cluster") == cluster.READY


def test_delete_unknown():
    # Does nothing, so should succeed.
    commands.run("drenv", "delete", "--name-prefix", "unknown-", EXTERNAL_ENV)


def test_delete(tmpenv):
    # Delete does nothing, so cluster must be ready.
    commands.run("drenv", "delete", "--name-prefix", tmpenv.prefix, EXTERNAL_ENV)
    assert cluster.status(tmpenv.prefix + "cluster") == cluster.READY


def test_missing_addon(tmpdir):
    content = """
name: missing-test
profiles:
  - name: cluster
    external: true
    workers:
      - addons:
          - name: no-such-addon
"""
    path = tmpdir.join("missing-addon.yaml")
    path.write(content)
    with pytest.raises(commands.Error):
        commands.run("drenv", "start", str(path))


def test_kustomization(tmpdir):
    content = """
key1: ${value1}
key2: ${value2}
"""
    template = tmpdir.join("kustomization.yaml")
    template.write(content)
    with drenv.kustomization(
        str(template), value1="value 1", value2="value 2"
    ) as kustomization_dir:
        path = os.path.join(kustomization_dir, "kustomization.yaml")
        with open(path) as f:
            content = f.read()

    expected = """
key1: value 1
key2: value 2
"""
    assert content == expected
    assert not os.path.exists(kustomization_dir)


def test_kustomization_yaml():
    content = """
key1: value 1
key2: value 2
"""
    with drenv.kustomization_yaml(content) as kustomization_dir:
        path = os.path.join(kustomization_dir, "kustomization.yaml")
        with open(path) as f:
            assert f.read() == content
    assert not os.path.exists(kustomization_dir)


def test_temporary_kubeconfig(tmpenv):
    orig_default_config = get_config(context=tmpenv.profile)
    print("orig_default_config", yaml.dump(orig_default_config))

    with drenv.temporary_kubeconfig("drenv-test.") as env:
        kubeconfig = env["KUBECONFIG"]
        assert os.path.isfile(kubeconfig)

        # When created both config are the same.
        temporary_config = get_config(context=tmpenv.profile, kubeconfig=kubeconfig)
        print("temporary_config", yaml.dump(temporary_config))
        assert temporary_config == orig_default_config

        # If we change the temporary kubeconfig, the default one is not modified.
        kubectl.config("use-context", tmpenv.profile, "--kubeconfig", kubeconfig)
        kubectl.config(
            "set-context",
            "--current",
            "--namespace=foobar",
            "--kubeconfig",
            kubeconfig,
        )
        current_default_config = get_config(context=tmpenv.profile)
        temporary_config = get_config(context=tmpenv.profile, kubeconfig=kubeconfig)
        print("temporary_config", yaml.dump(temporary_config))
        assert current_default_config == orig_default_config
        assert temporary_config["contexts"][0]["context"]["namespace"] == "foobar"
        assert temporary_config["current-context"] == tmpenv.profile


def get_config(context=None, kubeconfig=None):
    args = [
        "view",
        "--minify",
        "--output=json",
    ]
    if kubeconfig:
        args.append(f"--kubeconfig={kubeconfig}")
    out = kubectl.config(*args, context=context)
    return json.loads(out)
