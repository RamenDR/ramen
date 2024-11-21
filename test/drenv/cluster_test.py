# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import pytest

from drenv import cluster

pytestmark = pytest.mark.cluster


def test_status_unknown():
    assert cluster.status("no-such-profile") == cluster.UNKNOWN


def test_status_ready(tmpenv):
    assert cluster.status(tmpenv.profile) == cluster.READY


def test_wait_until_ready_unknown():
    with pytest.raises(RuntimeError):
        cluster.wait_until_ready("no-such-profile", timeout=0)


def test_wait_until_ready(tmpenv):
    cluster.wait_until_ready(tmpenv.profile, timeout=0)


def test_kubeconfig_unknown():
    cluster.kubeconfig("no-such-profile") == {}


def test_kubeconfig(tmpenv):
    cfg = cluster.kubeconfig(tmpenv.profile)
    assert cfg["name"] == tmpenv.profile
    assert "cluster" in cfg
