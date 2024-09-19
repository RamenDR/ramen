# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import platform
import selectors
import subprocess
import textwrap
import time

from . import shutdown

OUT = "out"
ERR = "err"

_Selector = getattr(selectors, "PollSelector", selectors.SelectSelector)

# Amount of data that can be written to a pipe without blocking, defined by
# POSIX to 512 but is 4096 on Linux. See pipe(7).
_PIPE_BUF = 4096 if platform.system() == "Linux" else 512


class Error(Exception):
    INDENT = 3 * " "

    def __init__(self, command, error, exitcode=None, output=None):
        self.command = command
        self.error = error
        self.exitcode = exitcode
        self.output = output

    def with_exception(self, exc):
        """
        Return a new error preserving the traceback from another excpetion.
        """
        self.__cause__ = None
        return self.with_traceback(exc.__traceback__)

    def _indent(self, s):
        return textwrap.indent(s, self.INDENT)

    def __str__(self):
        lines = [
            "Command failed:\n",
            self._indent(f"command: {self.command}\n"),
        ]

        if self.exitcode is not None:
            lines.append(self._indent(f"exitcode: {self.exitcode}\n"))

        if self.output:
            output = self._indent(self.output.rstrip())
            lines.append(self._indent(f"output:\n{output}\n"))

        error = self._indent(self.error.rstrip())
        lines.append(self._indent(f"error:\n{error}\n"))

        return "".join(lines)


class Timeout(Error):

    def __init__(self, command, error):
        super().__init__(command, error)


class StreamTimeout(Exception):
    """
    Raised when stream timeout expires.
    """


def run(*args, input=None, stdin=None, decode=True, env=None, cwd=None):
    """
    Run command args and return the output of the command.

    Assumes that the child process output UTF-8. Will raise if the command
    outputs binary data. This is not a problem in this projects since all our
    commands are text based.

    Invalid UTF-8 in child process stderr is handled gracefully so we don't
    fail to raise an error about the failing command. Invalid characters will
    be replaced with unicode replacement character (U+FFFD).

    Raises Error if creating a child process failed or the child process
    terminated with non-zero exit code. The error includes all data read from
    the child process stdout and stderr.
    """
    with shutdown.guard():
        try:
            p = subprocess.Popen(
                args,
                stdin=_select_stdin(input, stdin),
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                env=env,
                cwd=cwd,
            )
        except OSError as e:
            raise Error(args, f"Could not execute: {e}").with_exception(e)

    output, error = p.communicate(input=input.encode() if input else None)

    if p.returncode != 0:
        error = error.decode(errors="replace")
        raise Error(args, error, exitcode=p.returncode, output=output.decode())

    return output.decode() if decode else output


def watch(
    *args,
    input=None,
    keepends=False,
    decode=True,
    timeout=None,
    env=None,
    stdin=None,
    stderr=subprocess.PIPE,
    cwd=None,
):
    """
    Run command args, iterating over lines read from the child process stdout.

    Some commands have no output and log everyting to stderr (like drenv). To
    watch the output call with stderr=subprocess.STDOUT. When such command
    fails, we have always have empty error, since the content was already
    yielded to the caller.

    Assumes that the child process output UTF-8. Will raise if the command
    outputs binary data. This is not a problem in this projects since all our
    commands are text based.

    Invalid UTF-8 in child process stderr is handled gracefully so we don't
    fail to raise an error about the failing command. Invalid characters will
    be replaced with unicode replacement character (U+FFFD).

    The call return a generator object that can be used to iterate over lines.
    To stop watching early and terminate the process, call close() on the
    returned value.

    Raises:
    - Error if creating a child process failed or the child process
      terminated with non-zero exit code. The error includes all data read
      from the child process stderr.
    - Timeout if the command did not terminate within the specified
      timeout.
    """
    if env is None:
        env = dict(os.environ)

    # Avoid delays in python child process logs.
    env["PYTHONUNBUFFERED"] = "1"

    with shutdown.guard():
        try:
            p = subprocess.Popen(
                args,
                stdin=_select_stdin(input, stdin),
                stdout=subprocess.PIPE,
                stderr=stderr,
                env=env,
                cwd=cwd,
            )
        except OSError as e:
            raise Error(args, f"Could not execute: {e}").with_exception(e)

    try:
        error = bytearray()
        partial = bytearray()

        for src, data in stream(p, input=input, timeout=timeout):
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

                    if not keepends:
                        line = line.rstrip()
                    yield line.decode() if decode else line

        if partial:
            line = bytes(partial)
            del partial[:]
            if not keepends:
                line = line.rstrip()
            yield line.decode() if decode else line

    except GeneratorExit:
        p.kill()
        return
    except StreamTimeout as e:
        p.kill()
        raise Timeout(args, "Timed watching command").with_exception(e)
    finally:
        p.wait()

    if p.returncode != 0:
        error = error.decode(errors="replace")
        raise Error(args, error, exitcode=p.returncode)


def stream(proc, input=None, bufsize=32 << 10, timeout=None):
    """
    Stream data from process stdout and stderr.

    proc is a subprocess.Popen instance created with stdout=subprocess.PIPE and
    stderr=subprocess.PIPE. If only one stream is used don't use this, stream
    directly from the single pipe.

    If input is not None, proc must be created with stdin=subprocess.PIPE and
    the pipe must be open.

    Yields either (OUT, data) or (ERR, data) read from proc stdout and stderr.
    Returns when both streams are closed.
    """
    if timeout is None:
        deadline = None
    else:
        deadline = time.monotonic() + timeout

    if input:
        if proc.stdin is None:
            raise RuntimeError("Cannot stream input: proc.stdin is None")
        if proc.stdin.closed:
            raise RuntimeError("Cannot stream input: proc.stdin is closed")
    elif proc.stdin:
        try:
            proc.stdin.close()
        except BrokenPipeError:
            pass

    # Use only if input is not None, but it helps pylint.
    input_view = ""
    input_offset = 0

    with _Selector() as sel:
        for f, src in (proc.stdout, OUT), (proc.stderr, ERR):
            if f and not f.closed:
                sel.register(f, selectors.EVENT_READ, src)
        if input:
            sel.register(proc.stdin, selectors.EVENT_WRITE)
            input_view = memoryview(input.encode())

        while sel.get_map():
            remaining = _remaining_time(deadline)
            for key, event in sel.select(remaining):
                if key.fileobj is proc.stdin:
                    # Stream data from caller to child process.
                    chunk = input_view[input_offset : input_offset + _PIPE_BUF]
                    try:
                        input_offset += os.write(key.fd, chunk)
                    except BrokenPipeError:
                        sel.unregister(key.fileobj)
                        key.fileobj.close()
                    else:
                        if input_offset >= len(input_view):
                            sel.unregister(key.fileobj)
                            key.fileobj.close()
                else:
                    # Stream data from child process to caller.
                    data = os.read(key.fd, bufsize)
                    if not data:
                        sel.unregister(key.fileobj)
                        key.fileobj.close()
                        continue

                    yield key.data, data


def _select_stdin(input=None, stdin=None):
    if input and stdin:
        raise RuntimeError("intput and stdin are mutually exclusive")
    if input:
        return subprocess.PIPE
    if stdin:
        return stdin
    # Avoid blocking foerver if there is no input.
    return subprocess.DEVNULL


def _remaining_time(deadline):
    if deadline is None:
        return None

    now = time.monotonic()
    if now >= deadline:
        raise StreamTimeout

    return deadline - now
