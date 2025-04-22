// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"time"
)

const (
	Timeout       = 600 * time.Second
	RetryInterval = 5 * time.Second
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
