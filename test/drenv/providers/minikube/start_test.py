# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from collections import defaultdict
from types import SimpleNamespace
from unittest.mock import Mock

import pytest

from drenv.providers import minikube
from drenv import __main__ as main


@pytest.mark.parametrize("driver", ["vfkit", "kvm2", "docker", "podman"])
@pytest.mark.parametrize("managed", [True, False])
@pytest.mark.parametrize("mode", ["auto", "host", "static"])
def test_start_dns(monkeypatch, driver, managed, mode):
    profile = defaultdict(lambda: None, name="test", driver=driver)
    watch = Mock()
    detect = Mock(return_value=managed)
    monkeypatch.setattr(minikube, "_watch", watch)
    monkeypatch.setattr(minikube.dns, "is_managed_mac", detect)

    minikube.start(profile, dns_mode=mode)

    args = watch.call_args.args
    use_static = driver in ("vfkit", "kvm2") and (
        mode == "static" or (mode == "auto" and managed)
    )
    if use_static:
        index = args.index("--dns-servers")
        assert args[index + 1] == "8.8.8.8,1.1.1.1"
    else:
        assert "--dns-servers" not in args
    assert args[0] == "start"
    assert watch.call_args.kwargs == {"profile": "test"}
    if mode == "auto" and driver in ("vfkit", "kvm2"):
        detect.assert_called_once_with(profile)
    else:
        detect.assert_not_called()


def test_start_invalid_dns_mode(monkeypatch):
    watch = Mock()
    monkeypatch.setattr(minikube, "_watch", watch)
    with pytest.raises(RuntimeError, match="Invalid dns_mode"):
        minikube.start({"name": "test", "driver": "vfkit"}, dns_mode="invalid")
    watch.assert_not_called()


@pytest.mark.parametrize("mode", ["auto", "host", "static"])
def test_start_cluster_passes_dns_mode(monkeypatch, mode):
    provider = Mock()
    provider.exists.return_value = False
    monkeypatch.setattr(main.providers, "get", lambda name: provider)
    profile = {"name": "test", "provider": "minikube"}
    args = SimpleNamespace(timeout=60, local_registry=False, dns_mode=mode)

    main.start_cluster(profile, args=args)

    provider.start.assert_called_once_with(
        profile, verbose=True, timeout=60, local_registry=False, dns_mode=mode
    )
    provider.configure.assert_called_once_with(profile, existing=False)
