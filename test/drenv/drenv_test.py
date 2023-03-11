# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import pytest

from drenv import cluster
from drenv import commands


def test_start_unknown():
    # Cluster does not exists, so it should fail.
    with pytest.raises(commands.Error):
        commands.run("drenv", "start", "--name-prefix", "unknown-", "external.yaml")


def test_start(tmpenv):
    commands.run("drenv", "start", "--name-prefix", tmpenv.prefix, "external.yaml")
    assert cluster.status(tmpenv.prefix + "cluster") == cluster.READY


def test_stop_unknown():
    # Does nothing, so should succeed.
    commands.run("drenv", "stop", "--name-prefix", "unknown-", "external.yaml")


def test_stop(tmpenv):
    # Stop does nothing, so cluster must be ready.
    commands.run("drenv", "stop", "--name-prefix", tmpenv.prefix, "external.yaml")
    assert cluster.status(tmpenv.prefix + "cluster") == cluster.READY


def test_delete_unknown():
    # Does nothing, so should succeed.
    commands.run("drenv", "delete", "--name-prefix", "unknown-", "external.yaml")


def test_delete(tmpenv):
    # Delete does nothing, so cluster must be ready.
    commands.run("drenv", "delete", "--name-prefix", tmpenv.prefix, "external.yaml")
    assert cluster.status(tmpenv.prefix + "cluster") == cluster.READY
