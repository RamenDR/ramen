# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import subprocess
from contextlib import contextmanager

import pytest

from drenv import commands

# Streaming data from commands.


def test_stream_nothing():
    with run("true") as p:
        stream = list(commands.stream(p))
    assert stream == []


def test_stream_stdout():
    with run("echo", "-n", "output") as p:
        stream = list(commands.stream(p))
    assert stream == [(commands.OUT, b"output")]


def test_stream_stderr():
    with run("sh", "-c", "echo -n error >&2") as p:
        stream = list(commands.stream(p))
    assert stream == [(commands.ERR, b"error")]


def test_stream_both():
    with run("sh", "-c", "echo -n output; echo -n error >&2") as p:
        stream = list(commands.stream(p))
    assert stream == [(commands.OUT, b"output"), (commands.ERR, b"error")]


def test_stream_output_large():
    out = err = 0
    with run("dd", "if=/dev/zero", "bs=1M", "count=100", "status=none") as p:
        for src, data in commands.stream(p):
            if src == commands.OUT:
                out += len(data)
            else:
                err += len(data)
    assert out == 100 << 20
    assert err == 0


def test_stream_no_stdout():
    # No reason to stream with one pipe, but it works.
    with run("sh", "-c", "echo -n error >&2", stdout=None) as p:
        stream = list(commands.stream(p))
    assert stream == [(commands.ERR, b"error")]


def test_stream_no_stderr():
    # No reason to stream with one pipe, but it works.
    with run("sh", "-c", "echo -n output", stderr=None) as p:
        stream = list(commands.stream(p))
    assert stream == [(commands.OUT, b"output")]


def test_stream_no_stdout_stderr():
    # No reason without pipes, but it works.
    with run("true", stdout=None, stderr=None) as p:
        stream = list(commands.stream(p))
    assert stream == []


# Watching commands.


def test_watch_no_output():
    output = list(commands.watch("true"))
    assert output == []


def test_watch_lines():
    script = """
for i in range(10):
    print(f"line {i}", flush=True)
"""
    output = list(commands.watch("python3", "-c", script))
    assert output == ["line %d" % i for i in range(10)]


def test_watch_partial_lines():
    script = """
import time

print("first ", end="", flush=True);
time.sleep(0.02)
print("line\\nsecond ", end="", flush=True);
time.sleep(0.02)
print("line\\n", end="", flush=True);
"""
    output = list(commands.watch("python3", "-c", script))
    assert output == ["first line", "second line"]


def test_watch_no_newline():
    script = """
import time

print("first ", end="", flush=True);
time.sleep(0.02)
print("second ", end="", flush=True);
time.sleep(0.02)
print("last", end="", flush=True);
"""
    output = list(commands.watch("python3", "-c", script))
    assert output == ["first second last"]


def test_watch_error_empty():
    cmd = ("false",)
    output = []

    with pytest.raises(commands.Error) as e:
        for line in commands.watch(*cmd):
            output.append(line)

    assert output == []

    assert e.value.command == cmd
    assert e.value.exitcode == 1
    assert e.value.error == ""


def test_watch_error():
    cmd = ("sh", "-c", "echo -n output >&1; echo -n error >&2; exit 1")
    output = []

    with pytest.raises(commands.Error) as e:
        for line in commands.watch(*cmd):
            output.append(line)

    # All output is always reported to the caller, even if the command failed.
    assert output == ["output"]

    # Errors are buffered and reported only if the command failed.
    assert e.value.command == cmd
    assert e.value.exitcode == 1
    assert e.value.error == "error"


def test_watch_non_ascii():
    script = 'print("\u05d0")'  # Hebrew Letter Alef (U+05D0)
    output = list(commands.watch("python3", "-c", script))
    assert output == ["\u05d0"]


def test_watch_invalid_utf8():
    script = """
import os
os.write(1, bytes([0xff]))
"""
    with pytest.raises(UnicodeDecodeError):
        list(commands.watch("python3", "-c", script))


def test_error():
    e = commands.Error(("arg1", "arg2"), exitcode=2, error="err 1\nerr 2\n")
    expected = """\
Command failed:
   command: ('arg1', 'arg2')
   exitcode: 2
   error:
      err 1
      err 2
"""
    assert str(e) == expected


@contextmanager
def run(*args, stdout=subprocess.PIPE, stderr=subprocess.PIPE):
    p = subprocess.Popen(args, stdout=stdout, stderr=stderr)
    try:
        yield p
    finally:
        p.wait()
