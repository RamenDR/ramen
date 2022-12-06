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

    def __init__(self, command, exitcode, error):
        self.command = command
        self.exitcode = exitcode
        self.error = error

    def __str__(self):
        error = textwrap.indent(self.error.rstrip(), self.INDENT * 2)
        return f"""\
Command failed:
   command: {self.command}
   exitcode: {self.exitcode}
   error:
{error}
"""


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
