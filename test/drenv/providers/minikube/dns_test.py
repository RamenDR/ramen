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


@pytest.mark.parametrize("driver", ["vfkit", "kvm2", "docker", "podman"])
@pytest.mark.parametrize("active", [True, False])
def test_servers_auto(driver, active, monkeypatch):
    provider = FakeNetworkExtension(NetworkExtension(active=active, enabled=True))
    monkeypatch.setattr(networkextension, "list_extensions", provider.list_extensions)
    profile = {"name": "test", "driver": driver}
    expected = []
    if platform.system() == "Darwin" and active and driver in ("vfkit", "kvm2"):
        expected = ["8.8.8.8", "1.1.1.1"]
    assert dns.servers(profile, "auto") == expected


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
