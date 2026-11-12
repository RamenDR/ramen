// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"bytes"
	"context"
	"os/exec"
)

// FailedToStart is the exit code when the process failed to start.
const FailedToStart = -2

// Result holds the outcome of a command run by RunCommand.
// Stdout and Stderr are what the process wrote.
// ExitCode == 0 and Err == nil means the process exited successfully.
// ExitCode == -1 means the process was terminated by a signal.
// ExitCode == FailedToStart means the process failed to start.
type Result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	Err      error
}

// RunCommand runs a command. The process is killed when ctx is canceled or the deadline is exceeded.
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

	if ctx.Err() != nil {
		result.Err = ctx.Err()

		return result
	}

	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			result.ExitCode = ee.ExitCode()
		} else {
			result.ExitCode = FailedToStart
		}
	}

	return result
}
