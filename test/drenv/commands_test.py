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


def test_stream_input_empty():
    with run("cat", stdin=subprocess.PIPE) as p:
        stream = list(commands.stream(p, input=""))
    assert stream == []


def test_stream_input():
    with run("cat", stdin=subprocess.PIPE) as p:
        stream = list(commands.stream(p, input="input"))
    assert stream == [(commands.OUT, b"input")]


def test_stream_input_large():
    # Stream 10 MiB of data to ensure we don't deadlock when sending large
    # payloads.
    text = "A" * (10 << 20)
    out = bytearray()
    err = bytearray()

    with run("cat", stdin=subprocess.PIPE) as p:
        for src, data in commands.stream(p, input=text):
            if src == commands.OUT:
                out += data
            else:
                err += data

    assert err.decode() == ""
    assert out.decode() == text


def test_stream_input_no_stdin():
    with pytest.raises(RuntimeError):
        with run("cat", stdin=None) as p:
            list(commands.stream(p, input="input"))


def test_stream_input_stdin_closed():
    with pytest.raises(RuntimeError):
        with run("cat", stdin=subprocess.PIPE) as p:
            p.stdin.close()
            list(commands.stream(p, input="input"))


def test_stream_input_child_close_pipe():
    # Write 1 MiB to child that ignores the input and exits.  Should not
    # deadlock blocking on the pipe of fail when the pipe is closed before all
    # input was streamed.
    text = "A" * (1 << 20)
    with run("echo", "-n", "output", stdin=subprocess.PIPE) as p:
        stream = list(commands.stream(p, input=text))

    assert stream == [(commands.OUT, b"output")]


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


# Running commands.


def test_run():
    output = commands.run("true")
    assert output == ""


def test_run_input():
    output = commands.run("cat", input="input")
    assert output == "input"


def test_run_input_non_ascii():
    output = commands.run("cat", input="\u05d0")
    assert output == "\u05d0"


def test_run_error_empty():
    cmd = ("false",)
    with pytest.raises(commands.Error) as e:
        commands.run(*cmd)
    assert e.value.command == cmd
    assert e.value.exitcode == 1
    assert e.value.output == ""
    assert e.value.error == ""


def test_run_error():
    cmd = ("sh", "-c", "echo -n output >&1; echo -n error >&2; exit 1")

    with pytest.raises(commands.Error) as e:
        commands.run(*cmd)

    assert e.value.command == cmd
    assert e.value.exitcode == 1
    assert e.value.error == "error"
    assert e.value.output == "output"


def test_run_non_ascii():
    script = 'print("\u05d0")'  # Hebrew Letter Alef (U+05D0)
    output = commands.run("python3", "-c", script)
    assert output == "\u05d0\n"


def test_run_invalid_utf8():
    script = """
import os
os.write(1, bytes([0xff]))
"""
    with pytest.raises(UnicodeDecodeError):
        commands.run("python3", "-c", script)


# Formatting errors.


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


def test_error_with_output():
    e = commands.Error(
        ("arg1", "arg2"), exitcode=3, error="err 1\nerr 2\n", output="out 1\nout 2\n"
    )
    expected = """\
Command failed:
   command: ('arg1', 'arg2')
   exitcode: 3
   output:
      out 1
      out 2
   error:
      err 1
      err 2
"""
    assert str(e) == expected


@contextmanager
def run(*args, stdin=None, stdout=subprocess.PIPE, stderr=subprocess.PIPE):
    p = subprocess.Popen(args, stdin=stdin, stdout=stdout, stderr=stderr)
    try:
        yield p
    except BaseException:
        p.kill()
        raise
    finally:
        p.wait()
