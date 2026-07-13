# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import pytest

from drenv import kubectl


def test_apply_defaults_to_server_side(monkeypatch):
    calls = []
    monkeypatch.setattr(
        kubectl, "_watch", lambda *args, **kwargs: calls.append((args, kwargs))
    )

    kubectl.apply("--filename=resource.yaml", context="cluster")

    assert calls == [
        (
            ("apply", "--server-side=true", "--filename=resource.yaml"),
            {"input": None, "context": "cluster", "log": print},
        )
    ]


def test_apply_can_disable_server_side(monkeypatch):
    calls = []
    monkeypatch.setattr(
        kubectl, "_watch", lambda *args, **kwargs: calls.append((args, kwargs))
    )

    kubectl.apply("--filename=resource.yaml", server_side=False)

    assert calls[0][0] == ("apply", "--filename=resource.yaml")


@pytest.mark.parametrize("flag", ["--server-side", "--server-side=false"])
def test_apply_rejects_raw_server_side_flags(monkeypatch, flag):
    monkeypatch.setattr(
        kubectl,
        "_watch",
        lambda *args, **kwargs: pytest.fail("kubectl must not be called"),
    )

    with pytest.raises(
        ValueError,
        match="use server_side argument instead of --server-side flag",
    ):
        kubectl.apply("--filename=resource.yaml", flag)
