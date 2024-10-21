// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package testing

import (
	"fmt"
	"testing"

	"github.com/ramendr/ramen/e2e/util"
)

// Fatal logs an error and fails the test immediately.
func Fatal(t *testing.T, err error, msg string) {
	t.Helper()
	util.Ctx.Log.Error(err, msg)
	t.FailNow()
}

// Fatalf logs a formatted error and fails the test immediately.
func Fatalf(t *testing.T, err error, format string, args ...any) {
	t.Helper()
	util.Ctx.Log.Error(err, fmt.Sprintf(format, args...))
	t.FailNow()
}

// Error logs an error message and mark the test as failed.
func Error(t *testing.T, err error, msg string) {
	t.Helper()
	util.Ctx.Log.Error(err, msg)
	t.Fail()
}

// Errorf logs a formatted error message and mark the test as failed.
func Errorf(t *testing.T, err error, format string, args ...any) {
	t.Helper()
	util.Ctx.Log.Error(err, fmt.Sprintf(format, args...))
	t.Fail()
}

// Skip logs a message and skips the test.
func Skip(t *testing.T, msg string) {
	t.Helper()
	util.Ctx.Log.Info(msg)
	t.SkipNow()
}

// Skipf logs a formatted message the skips the test.
func Skipf(t *testing.T, format string, args ...any) {
	t.Helper()
	util.Ctx.Log.Info(fmt.Sprintf(format, args...))
	t.SkipNow()
}

// Fail marks the test as failed.
func Fail(t *testing.T) {
	t.Helper()
	t.Fail()
}

// FailNow fails the tests immedatialy.
func FailNow(t *testing.T) {
	t.Helper()
	t.FailNow()
}
