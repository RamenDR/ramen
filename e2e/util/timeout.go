// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"time"
)

const (
	Timeout       = 10 * time.Minute
	RetryInterval = 5 * time.Second

	// UnprotectTimeout defines the maximum time allowed for resource deletion during unprotect.
	// This includes waiting for DRPCs and related resources to be fully removed.
	UnprotectTimeout = 10 * time.Minute

	// UndeployTimeout defines the maximum time allowed for all resource deletions during undeploy.
	// TODO: Undeploy usually takes about 10 seconds, we keep it longer because we call it in
	// parallel with unprotect in ramenctl during cleanup and it needs more testing.
	UndeployTimeout = 3 * time.Minute

	// CleanupTimeout defines the maximum time allowed for resource cleanup operations like channels.
	CleanupTimeout = 1 * time.Minute
)

// Sleep pauses the current goroutine for at least the duration d. If the context was canceled or its deadline has
// exceeded it will return early with a context.Canceled or a context.DeadlineExceeded error.
func Sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
