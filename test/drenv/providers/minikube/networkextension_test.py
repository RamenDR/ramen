# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os

from . import networkextension


def test_parse_enabled_and_active():
    out = _load_testdata("enabled-and-active.stdout")
    extensions = networkextension.parse_output(out)
    assert extensions == [
        networkextension.NetworkExtension(
            enabled=True,
            active=True,
            team_id="DE8Y96K9QP",
            bundle_id="com.cisco.anyconnect.macos.acsockext (5.1.3.14/5.1.3.14)",
            name="Cisco Secure Client - Socket Filter Extension",
            state="activated enabled",
        ),
        networkextension.NetworkExtension(
            enabled=True,
            active=True,
            team_id="X9E956P446",
            bundle_id="com.crowdstrike.falcon.Agent (7.30/202.02)",
            name="Falcon Sensor",
            state="activated enabled",
        ),
    ]


def test_parse_disabled_or_inactive():
    out = _load_testdata("disabled-or-inactive.stdout")
    extensions = networkextension.parse_output(out)
    assert len(extensions) == 2

    assert extensions[0].enabled is False
    assert extensions[0].active is True
    assert extensions[0].team_id == "DE8Y96K9QP"

    assert extensions[1].enabled is True
    assert extensions[1].active is False
    assert extensions[1].team_id == "X9E956P446"


def test_parse_disabled_and_inactive():
    out = _load_testdata("disabled-and-inactive.stdout")
    extensions = networkextension.parse_output(out)

    assert len(extensions) == 2

    assert extensions[0].enabled is False
    assert extensions[0].active is False
    assert extensions[0].team_id == "DE8Y96K9QP"

    assert extensions[1].enabled is False
    assert extensions[1].active is False
    assert extensions[1].team_id == "X9E956P446"


def test_parse_no_extesions():
    out = _load_testdata("no-extesions.stdout")
    assert networkextension.parse_output(out) == []


def _load_testdata(name):
    path = os.path.join(os.path.dirname(__file__), "testdata", name)
    with open(path) as f:
        return f.read()
