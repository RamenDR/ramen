# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import selectors
import subprocess
import textwrap

OUT = "out"
ERR = "err"

_Selector = getattr(selectors, "PollSelector", selectors.SelectSelector)


class Error(Exception):
    INDENT = 3 * " "

    def __init__(self, command, exitcode, error, output=None):
        self.command = command
        self.exitcode = exitcode
        self.error = error
        self.output = output

    def _indent(self, s):
        return textwrap.indent(s, self.INDENT)

    def __str__(self):
        lines = [
            "Command failed:\n",
            self._indent(f"command: {self.command}\n"),
            self._indent(f"exitcode: {self.exitcode}\n"),
        ]

        if self.output:
            output = self._indent(self.output.rstrip())
            lines.append(self._indent(f"output:\n{output}\n"))

        error = self._indent(self.error.rstrip())
        lines.append(self._indent(f"error:\n{error}\n"))

        return "".join(lines)


def run(*args, input=None):
    """
    Run command args and return the output of the command.

    Assumes that the child process output UTF-8. Will raise if the command
    outputs binary data. This is not a problem in this projects since all our
    commands are text based.

    Invalid UTF-8 in child process stderr is handled gracefully so we don't
    fail to raise an error about the failing command. Invalid characters will
    be replaced with unicode replacement character (U+FFFD).

    Raises Error if the child process terminated with non-zero exit code. The
    error includes all data read from the child process stdout and stderr.
    """
    cp = subprocess.run(
        args,
        input=input.encode() if input else None,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    output = cp.stdout.decode()
    if cp.returncode != 0:
        error = cp.stderr.decode(errors="replace")
        raise Error(args, cp.returncode, error, output=output)
    return output


def watch(*args):
    """
    Run command args, iterating over lines read from the child process stdout.

    Assumes that the child process output UTF-8. Will raise if the command
    outputs binary data. This is not a problem in this projects since all our
    commands are text based.

    Invalid UTF-8 in child process stderr is handled gracefully so we don't
    fail to raise an error about the failing command. Invalid characters will
    be replaced with unicode replacement character (U+FFFD).

    Raises Error if the child process terminated with non-zero exit code. The
    error includes all data read from the child process stderr.
    """
    # Avoid delays in python child process logs.
    env = dict(os.environ)
    env["PYTHONUNBUFFERED"] = "1"

    with subprocess.Popen(
        args,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
    ) as p:
        error = bytearray()
        partial = bytearray()

        for src, data in stream(p):
            if src is ERR:
                error += data
            else:
                for line in data.splitlines(keepends=True):
                    if not line.endswith(b"\n"):
                        partial += line
                        break

                    if partial:
                        line = bytes(partial) + line
                        del partial[:]

                    yield line.rstrip().decode()

        if partial:
            yield partial.rstrip().decode()
            del partial[:]

    if p.returncode != 0:
        error = error.decode(errors="replace")
        raise Error(args, p.returncode, error)


def stream(proc, bufsize=32 << 10):
    """
    Stream data from process stdout and stderr.

    proc is a subprocess.Popen instance created with stdout=subprocess.PIPE and
    stderr=subprocess.PIPE. If only one stream is used don't use this, stream
    directly from the single pipe.

    Yields either (OUT, data) or (ERR, data) read from proc stdout and stderr.
    Returns when both streams are closed.
    """
    with _Selector() as sel:
        for f, src in (proc.stdout, OUT), (proc.stderr, ERR):
            if f and not f.closed:
                sel.register(f, selectors.EVENT_READ, src)

        while sel.get_map():
            for key, event in sel.select():
                data = os.read(key.fd, bufsize)
                if not data:
                    sel.unregister(key.fileobj)
                    key.fileobj.close()
                    continue

                yield key.data, data
