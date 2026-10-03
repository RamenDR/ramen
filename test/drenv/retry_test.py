# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import time
import pytest

from drenv import retry


class Error(Exception):
    """
    Raised on Function errors.
    """


class Function:

    def __init__(self, errors=0):
        self.errors = errors
        self.calls = []

    def __call__(self, *args, **kwargs):
        self.calls.append((args, kwargs))
        if self.errors > 0:
            self.errors -= 1
            raise Error("Simulated error")

        return True


def test_success():
    func = Function()
    assert retry.on_error(func)
    assert func.calls == [((), {})]


def test_success_with_args():
    func = Function()
    assert retry.on_error(func, args=(1, 2), kwargs={"k": "v"})
    assert func.calls == [((1, 2), {"k": "v"})]


def test_error():
    func = Function(errors=1)
    assert retry.on_error(func, duration=0.0)
    # First call fails, second call succeeds.
    assert func.calls == [((), {}), ((), {})]


def test_error_args():
    func = Function(errors=1)
    assert retry.on_error(func, args=(1, 2), kwargs={"k": "v"}, duration=0)
    # First call fails, second call succeeds.
    assert func.calls == [((1, 2), {"k": "v"}), ((1, 2), {"k": "v"})]


def test_error_count():
    # Must fail on the third call.
    func = Function(errors=3)
    with pytest.raises(Error):
        retry.on_error(func, count=2, duration=0.0)


def test_error_factor(monkeypatch):
    # Duration must increase by factor.
    sleeps = []
    monkeypatch.setattr(time, "sleep", sleeps.append)
    func = Function(errors=4)
    assert retry.on_error(func, duration=0.01, factor=10)
    assert sleeps == [0.01, 0.1, 1.0, 10.0]


def test_error_cap(monkeypatch):
    # Duration must not increase above cap.
    sleeps = []
    monkeypatch.setattr(time, "sleep", sleeps.append)
    func = Function(errors=4)
    assert retry.on_error(func, factor=10)
    assert sleeps == [1.0, 10.0, 30.0, 30.0]


def test_error_specific_retry():
    # Must retry on Error and succeed on second attempt.
    func = Function(errors=1)
    assert retry.on_error(func, error=Error, duration=0.0)


def test_error_specific_fail():
    # Must fail on the first error since Error is not a RuntimeError.
    func = Function(errors=1)
    with pytest.raises(Error):
        retry.on_error(func, error=RuntimeError)
