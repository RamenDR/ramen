# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import subprocess
from contextlib import contextmanager

from drenv import commands


def test_nothing():
    with run("true") as p:
        stream = list(commands.stream(p))
    assert stream == []


def test_stdout():
    with run("echo", "-n", "output") as p:
        stream = list(commands.stream(p))
    assert stream == [(commands.OUT, b"output")]


def test_stderr():
    with run("sh", "-c", "echo -n error >&2") as p:
        stream = list(commands.stream(p))
    assert stream == [(commands.ERR, b"error")]


def test_both():
    with run("sh", "-c", "echo -n output; echo -n error >&2") as p:
        stream = list(commands.stream(p))
    assert stream == [(commands.OUT, b"output"), (commands.ERR, b"error")]


def test_large_output():
    out = err = 0
    with run("dd", "if=/dev/zero", "bs=1M", "count=100", "status=none") as p:
        for src, data in commands.stream(p):
            if src == commands.OUT:
                out += len(data)
            else:
                err += len(data)
    assert out == 100 << 20
    assert err == 0


def test_no_stdout():
    # No reason to stream with one pipe, but it works.
    with run("sh", "-c", "echo -n error >&2", stdout=None) as p:
        stream = list(commands.stream(p))
    assert stream == [(commands.ERR, b"error")]


def test_no_stderr():
    # No reason to stream with one pipe, but it works.
    with run("sh", "-c", "echo -n output", stderr=None) as p:
        stream = list(commands.stream(p))
    assert stream == [(commands.OUT, b"output")]


def test_no_streams():
    # No reason without pipes, but it works.
    with run("true", stdout=None, stderr=None) as p:
        stream = list(commands.stream(p))
    assert stream == []


@contextmanager
def run(*args, stdout=subprocess.PIPE, stderr=subprocess.PIPE):
    p = subprocess.Popen(args, stdout=stdout, stderr=stderr)
    try:
        yield p
    finally:
        p.wait()
