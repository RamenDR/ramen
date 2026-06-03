# timestamp

Prefix each line of `stdout` and `stderr` with elapsed seconds since the command
started (`[seconds.mmm]`). Useful for profiling commands that do not log timing
themselves, such as `podman build`, `minikube start`, or long CI steps.

## Build

```console
make
```

The binary is `timestamp` in this directory (gitignored).

## Test

```console
make test
```

Builds `timestamp` first, then runs the tests.

## Usage

```console
./timestamp <command> [arguments...]
```

Example:

```console
$ ./timestamp bash -c 'echo first; echo second; sleep 1.234; echo last'
[    0.000] bash -c "echo first; echo second; sleep 1.234; echo last"
[    0.020] first
[    0.020] second
[    1.259] last
```

The first line is printed at `0.000` with the command being timed. Without it,
the first timestamp appears only when the command prints output, hiding startup
time.

Exit status matches the wrapped command.

## Notes

- Timestamps are per line (`\n`). Output without a newline is stamped when the
  process exits. Elapsed time is measured from when the child process starts,
  with millisecond precision.

- The child uses pipes, not a TTY, so programs may buffer output. Timestamps
  reflect when this tool reads a line, not when the child wrote it.

- Stdin is passed through so the command can consume stdin. When a command stops
  to wait for input, a plain wall-clock timer includes the wait; with
  `timestamp` you can see exactly when output resumes after the prompt.

- Arguments are quoted when needed so the line is copy-pasteable in a shell.
