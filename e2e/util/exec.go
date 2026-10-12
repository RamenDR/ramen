// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"
	"os/exec"
)

// RunCommand runs a command and return the error. The command will be killed when the context is canceled or the
// deadline is exceeded.
func RunCommand(ctx context.Context, command string, args ...string) error {
	cmd := exec.CommandContext(ctx, command, args...)

	// Run the command in a new process group so it is not terminated by the shell when the user interrupt go test.
	cmd.SysProcAttr = &runInBackground

	if out, err := cmd.Output(); err != nil {
		// If the context was canceled or the deadline exceeded, ignore the unhelpful "killed" error from the command.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("%w: stdout=%q stderr=%q", err, out, ee.Stderr)
		}

		return err
	}

	return nil
}

// KubectlKustomize runs "kubectl kustomize url" and returns the rendered manifests.
// When the command fails with a non-empty stderr, that stderr is included in the returned error.
func KubectlKustomize(ctx context.Context, url string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "kustomize", url)

	out, err := cmd.Output()
	if err != nil {
		return nil, commandError(ctx, err)
	}

	return out, nil
}

// commandError prefers the context error when the context ended the command. A non-empty stderr from an
// *exec.ExitError is included so callers can see why kubectl failed.
func commandError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return fmt.Errorf("%w: stderr=%q", err, ee.Stderr)
	}

	return err
}
