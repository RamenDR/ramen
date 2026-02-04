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


@requires_darwin
def test_is_managed_mac_some_active_and_enabled():
    provider = FakeNetworkExtension(
        NetworkExtension(active=True, enabled=True),
        NetworkExtension(active=False, enabled=True),
    )
    assert dns.is_managed_mac(provider) is True


@requires_darwin
def test_is_managed_mac_no_active_and_enabled():
    provider = FakeNetworkExtension(
        NetworkExtension(active=False, enabled=True),
        NetworkExtension(active=True, enabled=False),
    )
    assert dns.is_managed_mac(provider) is False


@requires_darwin
def test_is_managed_mac_no_extensions():
    assert dns.is_managed_mac(FakeNetworkExtension()) is False


@requires_linux
def test_is_managed_mac_not_darwin():
    provider = FakeNetworkExtension(NetworkExtension(active=True, enabled=True))
    assert dns.is_managed_mac(provider) is False
