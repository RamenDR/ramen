// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"fmt"
	"testing"
)

// Fatal logs an error and fails the test.
func Fatal(t *testing.T, msg string, err error) {
	t.Helper()
	Ctx.Log.Error(err, msg)
	t.FailNow()
}

// FailNow fails the tests silently. Use for parent tests.
func FailNow(t *testing.T) {
	t.Helper()
	t.FailNow()
}

// Skipf logs formmatted message the skips the test.
func Skipf(t *testing.T, format string, args ...any) {
	t.Helper()
	Skip(t, fmt.Sprintf(format, args...))
}

// Skip log msg and skips the test.
func Skip(t *testing.T, msg string) {
	t.Helper()
	Ctx.Log.Info(msg)
	t.SkipNow()
}
