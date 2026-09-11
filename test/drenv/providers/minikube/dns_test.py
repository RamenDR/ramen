# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import platform

import pytest

from . import dns
from . import networkextension

requires_darwin = pytest.mark.skipif(
    platform.system() != "Darwin",
    reason="requires Darwin (macOS)",
)
requires_linux = pytest.mark.skipif(
    platform.system() != "Linux",
    reason="requires Linux",
)
requires_not_darwin = pytest.mark.skipif(
    platform.system() == "Darwin",
    reason="requires an OS other than Darwin (macOS)",
)

NetworkExtension = networkextension.NetworkExtension


class FakeNetworkExtension:
    def __init__(self, *extensions):
        self._extensions = list(extensions)

    def list_extensions(self):
        return self._extensions


PROFILE = {"name": "test"}


@pytest.mark.parametrize("driver", ["vfkit", "kvm2", "docker", "podman"])
def test_servers_host(driver):
    assert dns.servers({"name": "test", "driver": driver}, "host") == []


@pytest.mark.parametrize("driver", ["vfkit", "kvm2"])
def test_servers_static_vm(driver):
    assert dns.servers({"name": "test", "driver": driver}, "static") == [
        "8.8.8.8",
        "1.1.1.1",
    ]


@pytest.mark.parametrize("driver", ["docker", "podman"])
def test_servers_static_container(driver, caplog):
    assert dns.servers({"name": "test", "driver": driver}, "static") == []
    assert f"static dns mode not supported for driver '{driver}'" in caplog.text


@requires_darwin
@pytest.mark.parametrize(
    "test",
    [
        dict(driver="vfkit", active=True, servers=list(dns.SERVERS)),
        dict(driver="vfkit", active=False, servers=[]),
        dict(driver="kvm2", active=True, servers=list(dns.SERVERS)),
        dict(driver="kvm2", active=False, servers=[]),
        dict(driver="docker", active=True, servers=[]),
        dict(driver="docker", active=False, servers=[]),
        dict(driver="podman", active=True, servers=[]),
        dict(driver="podman", active=False, servers=[]),
    ],
)
def test_servers_auto_darwin(test, monkeypatch):
    provider = FakeNetworkExtension(
        NetworkExtension(active=test["active"], enabled=True)
    )
    monkeypatch.setattr(networkextension, "list_extensions", provider.list_extensions)
    profile = {"name": "test", "driver": test["driver"]}
    assert dns.servers(profile, "auto") == test["servers"]


@requires_not_darwin
@pytest.mark.parametrize("driver", ["vfkit", "kvm2", "docker", "podman"])
def test_servers_auto_not_darwin(driver):
    assert dns.servers({"name": "test", "driver": driver}, "auto") == []


def test_servers_invalid_mode():
    with pytest.raises(RuntimeError, match="Invalid dns_mode 'invalid'"):
        dns.servers({"name": "test", "driver": "vfkit"}, "invalid")


@requires_darwin
def test_is_managed_mac_some_active_and_enabled():
    provider = FakeNetworkExtension(
        NetworkExtension(active=True, enabled=True),
        NetworkExtension(active=False, enabled=True),
    )
    assert dns.is_managed_mac(PROFILE, provider) is True


@requires_darwin
def test_is_managed_mac_no_active_and_enabled():
    provider = FakeNetworkExtension(
        NetworkExtension(active=False, enabled=True),
        NetworkExtension(active=True, enabled=False),
    )
    assert dns.is_managed_mac(PROFILE, provider) is False


@requires_darwin
def test_is_managed_mac_no_extensions():
    assert dns.is_managed_mac(PROFILE, FakeNetworkExtension()) is False


@requires_linux
def test_is_managed_mac_not_darwin():
    provider = FakeNetworkExtension(NetworkExtension(active=True, enabled=True))
    assert dns.is_managed_mac(PROFILE, provider) is False
