# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import collections
import os
import platform
import selectors
import signal
import subprocess
import time

from . import shutdown
from . import yaml

OUT = "out"
ERR = "err"

Failure = collections.namedtuple("Failure", ["command", "exitcode", "error"])

_Selector = getattr(selectors, "PollSelector", selectors.SelectSelector)

# Amount of data that can be written to a pipe without blocking, defined by
# POSIX to 512 but is 4096 on Linux. See pipe(7).
_PIPE_BUF = 4096 if platform.system() == "Linux" else 512

# Default buffer size for reading from child process.
_READ_BUF = 32 * 1024


class Error(Exception):

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

    def __str__(self):
        info = {"command": list(self.command)}
        if self.exitcode is not None:
            info["exitcode"] = self.exitcode
        if self.output:
            info["output"] = self.output.rstrip()
        if self.error:
            info["error"] = self.error.rstrip()
        return yaml.safe_dump({"command failed": info}).rstrip()


class PipelineError(Error):
    """
    Error raised when one or more commands in a pipeline fail.
    """

    def __init__(self, failures):
        """
        failures is a list of (command, exitcode, error) tuples.
        """
        self.failures = failures

    def __str__(self):
        items = []
        for failure in self.failures:
            item = {
                "command": list(failure.command),
                "exitcode": failure.exitcode,
            }
            if failure.error:
                item["error"] = failure.error.rstrip()
            items.append(item)
        return yaml.safe_dump({"pipeline failed": items}).rstrip()


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

        for _, src, data in _stream(p, input=input, timeout=timeout):
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


def pipeline(*commands, input=None, decode=True, timeout=None):
    """
    Run commands as a pipeline, piping stdout of each command to stdin of the
    next.

    If input is not None, it is written to the first command's stdin.

    Returns the output of the last command.

    Raises:
    - PipelineError if any command in the pipeline fails.
    - Timeout if the pipeline did not complete within the specified timeout.

    Example:
        pipeline(
            ["grep", "pattern"],
            ["sort"],
            input="line1\nline2\n",
        )
    """
    if len(commands) < 2:
        raise ValueError("pipeline requires at least 2 commands")

    procs = {}

    with shutdown.guard():
        last = None
        for cmd in commands:
            if last:
                stdin = last.stdout
            else:
                stdin = _select_stdin(input=input)

            try:
                p = subprocess.Popen(
                    cmd,
                    stdin=stdin,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                )
            except OSError as e:
                for proc in procs:
                    proc.kill()
                for proc in procs:
                    proc.wait()
                raise PipelineError([Failure(cmd, None, f"Could not execute: {e}")])

            # Close our reference so the previous process gets SIGPIPE if this
            # one exits.
            if last:
                last.stdout.close()

            procs[p] = bytearray()
            last = p

    # Collect output and stderr from all processes concurrently to avoid
    # deadlock when a process blocks on stderr write.
    output = bytearray()

    try:
        for proc, src, data in _stream(*procs, input=input, timeout=timeout):
            if src is OUT:
                output += data
            else:
                procs[proc] += data
    except StreamTimeout as e:
        for proc in procs:
            proc.kill()
        raise Timeout(commands, "Pipeline timed out").with_exception(e)
    except BaseException:
        for proc in procs:
            proc.kill()
        raise
    finally:
        for proc in procs:
            proc.wait()

    # Collect all failures. SIGPIPE in non-last commands is normal in
    # pipelines - it means a downstream command exited before consuming all
    # input, same as how real shells handle it.
    failures = []
    for proc, stderr in procs.items():
        if proc.returncode == 0:
            continue
        if proc is not last and proc.returncode == -signal.SIGPIPE:
            continue
        error = stderr.decode(errors="replace")
        failures.append(Failure(proc.args, proc.returncode, error))

    if failures:
        raise PipelineError(failures)

    return output.decode() if decode else bytes(output)


def _stream(*procs, input=None, timeout=None):
    """
    Stream data from one or more processes stdout and stderr.

    Each proc is a subprocess.Popen instance created with stdout=subprocess.PIPE
    and/or stderr=subprocess.PIPE.

    If input is not None, the first process must be created with
    stdin=subprocess.PIPE, and input is written to the first process stdin.

    Yields (proc, src, data) tuples where src is OUT or ERR.
    Returns when all streams are closed.
    """
    if timeout is None:
        deadline = None
    else:
        deadline = time.monotonic() + timeout

    first = procs[0]
    if input:
        if first.stdin is None:
            raise RuntimeError("Cannot stream input: first process stdin is None")
        if first.stdin.closed:
            raise RuntimeError("Cannot stream input: first process stdin is closed")
    elif first.stdin:
        try:
            first.stdin.close()
        except BrokenPipeError:
            pass

    input_view = memoryview(input.encode()) if input else None
    input_offset = 0

    with _Selector() as sel:
        for proc in procs:
            for f, src in (proc.stdout, OUT), (proc.stderr, ERR):
                if f and not f.closed:
                    sel.register(f, selectors.EVENT_READ, (proc, src))
        if input:
            sel.register(first.stdin, selectors.EVENT_WRITE)

        while sel.get_map():
            remaining = _remaining_time(deadline)
            for key, event in sel.select(remaining):
                if key.fileobj is first.stdin:
                    # Stream data from caller to first process.
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
                    data = os.read(key.fd, _READ_BUF)
                    if not data:
                        sel.unregister(key.fileobj)
                        key.fileobj.close()
                        continue

                    proc, src = key.data
                    yield proc, src, data


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
