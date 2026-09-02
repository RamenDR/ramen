// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

// Package olm_test tests OLM bundle image builds.
package olm_test

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestBundleBuild(t *testing.T) {
	runMake(t, "bundle-build")
}

func runMake(t *testing.T, target string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "make", target)
	cmd.Dir = "../.."

	out, err := cmd.CombinedOutput()
	t.Logf("make %s\n%s", target, out)

	if err != nil {
		t.Fatalf("make %s failed: %v\n%s", target, err, out)
	}
}
