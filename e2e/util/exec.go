// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"bytes"
	"context"
	"os/exec"
)

const (
	// FailedToStart is the exit code when the process failed to start.
	FailedToStart = -2

	// Signaled is the exit code when the process is terminated by a signal.
	Signaled = -1
)

// Result holds the outcome of a command run by RunCommand.
// Stdout and Stderr are what the process wrote.
// ExitCode == 0 and Err == nil means the process exited successfully.
// ExitCode == Signaled means the process was terminated by a signal.
// ExitCode == FailedToStart means the process failed to start.
// When ctx is canceled or its deadline is exceeded, Err is ctx.Err().
// ExitCode still reports how the process ended.
type Result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	Err      error
}

// RunCommand runs a command. The process is killed when ctx is canceled or the deadline is exceeded.
// Err is then ctx.Err(), and ExitCode reports how the process ended.
func RunCommand(ctx context.Context, name string, args ...string) Result {
	cmd := exec.CommandContext(ctx, name, args...)

	// Run the command in a new process group so it is not terminated by the shell when the user interrupt go test.
	cmd.SysProcAttr = &runInBackground

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := Result{
		Stdout: stdout.Bytes(),
		Stderr: stderr.Bytes(),
		Err:    err,
	}

	// Take the exit code from the process error before a context error replaces it.
	// Otherwise a killed process, or one that never started, reports exit code 0.
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			result.ExitCode = ee.ExitCode()
		} else {
			result.ExitCode = FailedToStart
		}
	}

	if ctx.Err() != nil {
		result.Err = ctx.Err()
	}

	return result
}
