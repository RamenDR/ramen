// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ramendr/ramen/e2e/util"
)

const (
	commandTimeout = time.Second
)

// result omits Err: each test checks the error separately.
type result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// Test that a silent, successful command returns exit code zero and empty output.
func TestRunCommandSucceedsWithNoOutput(t *testing.T) {
	res := util.RunCommand(t.Context(), "bash", "-c", "true")
	if res.Err != nil {
		t.Errorf("unexpected error: %v", res.Err)
	}

	checkResult(t, res, result{})
}

// Test that stdout and stderr stay separate even when the command succeeds.
func TestRunCommandCapturesStdoutAndStderr(t *testing.T) {
	res := util.RunCommand(t.Context(), "bash", "-c", `printf 'out\n'; printf 'err\n' >&2`)
	if res.Err != nil {
		t.Errorf("unexpected error: %v", res.Err)
	}

	checkResult(t, res, result{Stdout: []byte("out\n"), Stderr: []byte("err\n")})
}

// Test that a nonzero exit returns an *exec.ExitError without losing the exit code or output.
func TestRunCommandReportsNonzeroExit(t *testing.T) {
	res := util.RunCommand(t.Context(), "bash", "-c", `printf 'out\n'; printf 'err\n' >&2; exit 1`)
	if _, ok := res.Err.(*exec.ExitError); !ok {
		t.Errorf("Err: got %T, want *exec.ExitError", res.Err)
	}

	checkResult(t, res, result{
		ExitCode: 1,
		Stdout:   []byte("out\n"),
		Stderr:   []byte("err\n"),
	})
}

// Test that a command missing from PATH returns ErrNotFound and FailedToStart.
func TestRunCommandMissingExecutable(t *testing.T) {
	res := util.RunCommand(t.Context(), "no such command in the path")
	if !errors.Is(res.Err, exec.ErrNotFound) {
		t.Errorf("Err: got %v, want exec.ErrNotFound", res.Err)
	}

	checkResult(t, res, result{ExitCode: util.FailedToStart})
}

// Test that RunCommand reports the platform's exit status for a killed process.
func TestRunCommandKilled(t *testing.T) {
	path := build(t, "testdata/kill-self.go")

	ctx, cancel := context.WithTimeout(t.Context(), commandTimeout)
	defer cancel()

	res := util.RunCommand(ctx, path)
	if _, ok := res.Err.(*exec.ExitError); !ok {
		t.Errorf("Err: got %T, want *exec.ExitError", res.Err)
	}

	checkResult(t, res, result{ExitCode: killedExitCode()})
}

// Test that an already-canceled context prevents the command from starting.
func TestRunCommandCanceledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	res := util.RunCommand(ctx, "bash", "-c", "true")
	if !errors.Is(res.Err, context.Canceled) {
		t.Errorf("Err: got %v, want context.Canceled", res.Err)
	}

	checkResult(t, res, result{ExitCode: util.FailedToStart})
}

// Test that canceling a running command kills it but preserves output it already wrote.
func TestRunCommandCanceledWhileRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	timer := time.AfterFunc(500*time.Millisecond, cancel)
	defer timer.Stop()

	script := `
		printf 'out\n'
		printf 'err\n' >&2
		exec sleep 10
	`

	res := util.RunCommand(ctx, "bash", "-c", script)
	if !errors.Is(res.Err, context.Canceled) {
		t.Errorf("Err: got %v, want context.Canceled", res.Err)
	}

	checkResult(t, res, result{
		ExitCode: killedExitCode(),
		Stdout:   []byte("out\n"),
		Stderr:   []byte("err\n"),
	})
}

// Test that a deadline kills the running command and reports DeadlineExceeded as the cause.
func TestRunCommandDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	res := util.RunCommand(ctx, "bash", "-c", "exec sleep 10")
	if !errors.Is(res.Err, context.DeadlineExceeded) {
		t.Errorf("Err: got %v, want context.DeadlineExceeded", res.Err)
	}

	checkResult(t, res, result{ExitCode: killedExitCode()})
}

// Test that output larger than a pipe buffer is captured on both streams without blocking.
func TestRunCommandCapturesLargeOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), commandTimeout)
	defer cancel()

	// Large enough to fill a pipe and block if RunCommand waits for exit before reading.
	const largeOutputBytes = 1048576

	// On Windows, Git Bash supplies dd and its MSYS runtime provides /dev/zero.
	script := fmt.Sprintf(`
		dd if=/dev/zero bs=%[1]d count=1 2>/dev/null
		dd if=/dev/zero bs=%[1]d count=1 >&2 2>/dev/null
	`, largeOutputBytes)

	res := util.RunCommand(ctx, "bash", "-e", "-c", script)
	if res.Err != nil {
		t.Errorf("unexpected error: %v", res.Err)
	}

	zeros := make([]byte, largeOutputBytes)
	checkResult(t, res, result{Stdout: zeros, Stderr: zeros})
}

// Test that arguments are not interpreted by a shell.
func TestRunCommandNoShellInterpretation(t *testing.T) {
	cases := []struct {
		name string
		arg  string
	}{
		{"embedded space", "hello world"},
		{"shell control operators", "a; b | c & d"},
		{"command substitution", "$(uname)"},
		{"backtick command substitution", "`date`"},
		{"parameter expansion", "$USER"},
		{"pathname expansion", "*"},
		{"single quotes", "'single'"},
		{"double quotes", `"double"`},
		{"backslash", `back\slash`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := util.RunCommand(t.Context(), "printf", "%s", tc.arg)
			if res.Err != nil {
				t.Errorf("unexpected error: %v", res.Err)
			}

			checkResult(t, res, result{Stdout: []byte(tc.arg)})
		})
	}
}

func build(t *testing.T, source string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "helper.exe")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", path, source)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", source, err, out)
	}

	return path
}

func killedExitCode() int {
	if runtime.GOOS == "windows" {
		return 1 // Go's Process.Kill calls TerminateProcess with exit code 1.
	}

	return util.Signaled
}

func checkResult(t *testing.T, got util.Result, want result) {
	t.Helper()

	if got.ExitCode != want.ExitCode {
		t.Errorf("ExitCode: got %d, want %d", got.ExitCode, want.ExitCode)
	}

	if !bytes.Equal(got.Stdout, want.Stdout) {
		t.Errorf("Stdout: got %.80q (%d bytes), want %.80q (%d bytes)",
			got.Stdout, len(got.Stdout), want.Stdout, len(want.Stdout))
	}

	if !bytes.Equal(got.Stderr, want.Stderr) {
		t.Errorf("Stderr: got %.80q (%d bytes), want %.80q (%d bytes)",
			got.Stderr, len(got.Stderr), want.Stderr, len(want.Stderr))
	}
}
