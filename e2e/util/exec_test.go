// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/ramendr/ramen/e2e/util"
)

const (
	// Large enough to fill a pipe and block if RunCommand waits for exit before reading.
	largeOutputBytes = 1 << 20

	nonzeroStatus = 7
	commandLimit  = 15 * time.Second
)

func TestRunCommandSucceedsWithNoOutput(t *testing.T) {
	res := util.RunCommand(t.Context(), "bash", "-c", "true")

	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}

	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}

	expectOutput(t, res, "", "")
}

func TestRunCommandCapturesStdoutAndStderr(t *testing.T) {
	res := util.RunCommand(t.Context(), "bash", "-c", `printf 'out\n'; printf 'err\n' >&2`)

	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}

	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}

	expectOutput(t, res, "out\n", "err\n")
}

func TestRunCommandReportsNonzeroExit(t *testing.T) {
	res := util.RunCommand(t.Context(), "bash", "-c", `printf 'out\n'; printf 'err\n' >&2; exit 7`)

	if res.Err == nil || res.Err.Error() != "exit status 7" {
		t.Fatalf("expected exit status 7 error, got %v", res.Err)
	}

	if res.ExitCode != nonzeroStatus {
		t.Fatalf("expected exit code %d, got %d", nonzeroStatus, res.ExitCode)
	}

	expectOutput(t, res, "out\n", "err\n")
}

func TestRunCommandMissingExecutable(t *testing.T) {
	res := util.RunCommand(t.Context(), "ramen-command-that-does-not-exist")

	if !errors.Is(res.Err, exec.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", res.Err)
	}

	if res.ExitCode != util.FailedToStart {
		t.Fatalf("expected exit code %d, got %d", util.FailedToStart, res.ExitCode)
	}
}

func TestRunCommandPermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("execute permission is unix-specific")
	}

	path := filepath.Join(t.TempDir(), "not-executable")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	res := util.RunCommand(t.Context(), path)

	if !errors.Is(res.Err, os.ErrPermission) {
		t.Fatalf("expected ErrPermission, got %v", res.Err)
	}

	if res.ExitCode != util.FailedToStart {
		t.Fatalf("expected exit code %d, got %d", util.FailedToStart, res.ExitCode)
	}
}

func TestRunCommandTerminatedBySignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal termination is unix-specific")
	}

	ctx, cancel := context.WithTimeout(t.Context(), commandLimit)
	defer cancel()

	res := util.RunCommand(ctx, os.Args[0], "helper", "signal")

	if res.ExitCode != util.Signaled {
		t.Fatalf("expected exit code %d, got %d", util.Signaled, res.ExitCode)
	}

	if res.Err == nil || res.Err.Error() != "signal: terminated" {
		t.Fatalf("expected signal: terminated error, got %v", res.Err)
	}
}

func TestRunCommandCanceledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	res := util.RunCommand(ctx, "bash", "-c", "exec sleep 60")

	if !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", res.Err)
	}

	if res.ExitCode != util.FailedToStart {
		t.Fatalf("expected exit code %d, got %d", util.FailedToStart, res.ExitCode)
	}

	expectOutput(t, res, "", "")
}

func TestRunCommandCanceledWhileRunning(t *testing.T) {
	res := runUntilReady(t, `touch "$1"; exec sleep 60`)

	if res.ExitCode != util.Signaled {
		t.Fatalf("expected exit code %d, got %d", util.Signaled, res.ExitCode)
	}

	expectOutput(t, res, "", "")
}

func TestRunCommandDeadlineExceeded(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	res := util.RunCommand(ctx, "bash", "-c", `touch "$1"; exec sleep 60`, "bash", ready)

	if _, err := os.Stat(ready); err != nil {
		t.Fatalf("expected ready file to exist, command error: %v", res.Err)
	}

	if !errors.Is(res.Err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", res.Err)
	}

	if res.ExitCode != util.Signaled {
		t.Fatalf("expected exit code %d, got %d", util.Signaled, res.ExitCode)
	}
}

func TestRunCommandPreservesOutputBeforeCancel(t *testing.T) {
	script := `printf 'out\n'; printf 'err\n' >&2; touch "$1"; exec sleep 60`
	res := runUntilReady(t, script)

	if res.ExitCode != util.Signaled {
		t.Fatalf("expected exit code %d, got %d", util.Signaled, res.ExitCode)
	}

	expectOutput(t, res, "out\n", "err\n")
}

func TestRunCommandCapturesLargeOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), commandLimit)
	defer cancel()

	res := util.RunCommand(ctx, os.Args[0], "helper", "large")

	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}

	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}

	if !bytes.Equal(res.Stdout, bytes.Repeat([]byte{'o'}, largeOutputBytes)) {
		t.Fatalf("unexpected stdout length %d", len(res.Stdout))
	}

	if !bytes.Equal(res.Stderr, bytes.Repeat([]byte{'e'}, largeOutputBytes)) {
		t.Fatalf("unexpected stderr length %d", len(res.Stderr))
	}
}

func TestRunCommandPassesArgumentsLiterally(t *testing.T) {
	res := util.RunCommand(
		t.Context(),
		"bash",
		"-c",
		`printf '%s\n' "$@"`,
		"bash",
		"hello world",
		"a;b|c&d",
		"$(uname)",
		"`date`",
		"*",
		"'single'",
		`"double"`,
		`back\slash`,
	)

	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}

	expected := "hello world\n" +
		"a;b|c&d\n" +
		"$(uname)\n" +
		"`date`\n" +
		"*\n" +
		"'single'\n" +
		"\"double\"\n" +
		"back\\slash\n"

	if string(res.Stdout) != expected {
		t.Fatalf("expected stdout:\n%s\ngot:\n%s", expected, res.Stdout)
	}

	if len(res.Stderr) != 0 {
		t.Fatalf("expected empty stderr, got %q", res.Stderr)
	}
}

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "helper" {
		os.Exit(helper(os.Args[2:]))
	}

	os.Exit(m.Run())
}

func runUntilReady(t *testing.T, script string) util.Result {
	t.Helper()

	ready := filepath.Join(t.TempDir(), "ready")

	parent, stop := context.WithTimeout(t.Context(), commandLimit)
	defer stop()

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	go cancelWhenReady(parent, cancel, ready)

	res := util.RunCommand(ctx, "bash", "-c", script, "bash", ready)
	if !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", res.Err)
	}

	return res
}

func cancelWhenReady(ctx context.Context, cancel context.CancelFunc, path string) {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := os.Stat(path); err == nil {
				cancel()

				return
			}
		}
	}
}

func expectOutput(t *testing.T, res util.Result, stdout, stderr string) {
	t.Helper()

	if string(res.Stdout) != stdout {
		t.Fatalf("expected stdout %q, got %q", stdout, res.Stdout)
	}

	if string(res.Stderr) != stderr {
		t.Fatalf("expected stderr %q, got %q", stderr, res.Stderr)
	}
}

func helper(args []string) int {
	if len(args) == 0 {
		return 1
	}

	switch args[0] {
	case "large":
		if len(args) != 1 {
			return 1
		}

		return writeLarge()
	case "signal":
		if len(args) != 1 {
			return 1
		}

		return terminateBySignal()
	default:
		return 1
	}
}

func writeLarge() int {
	buf := bytes.Repeat([]byte{'o'}, largeOutputBytes)
	if _, err := os.Stdout.Write(buf); err != nil {
		return 1
	}

	for i := range buf {
		buf[i] = 'e'
	}

	if _, err := os.Stderr.Write(buf); err != nil {
		return 1
	}

	return 0
}

// terminateBySignal restores the default SIGTERM action and signals this process.
// The Go runtime otherwise catches SIGTERM and exits with a status.
func terminateBySignal() int {
	signal.Reset(syscall.SIGTERM)

	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		return 1
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return 1
	}

	for {
		time.Sleep(time.Hour)
	}
}
