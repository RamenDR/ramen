// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package test

import (
	"testing"

	"go.uber.org/zap"
)

// T extends testing.T to use a custom logger.
type T struct {
	*testing.T
	log *zap.SugaredLogger
}

// WithLog returns a t wrapped with a specified log.
//
//nolint:thelper
func WithLog(t *testing.T, log *zap.SugaredLogger) *T {
	return &T{T: t, log: log}
}

// Log writes a message to the log.
func (t *T) Log(msg string) {
	t.log.Info(msg)
}

// Log writes a formatted message to the log.
func (t *T) Logf(format string, args ...any) {
	t.log.Infof(format, args...)
}

// Error writes an error message to the log and mark the test as failed.
func (t *T) Error(msg string) {
	t.log.Error(msg)
	t.Fail()
}

// Errorf writes a formatted error message to the log and markd the test as failed.
func (t *T) Errorf(format string, args ...any) {
	t.log.Errorf(format, args...)
	t.Fail()
}

// Fatal writes an error message to the log and fail the text immediately.
func (t *T) Fatal(msg string) {
	t.log.Error(msg)
	t.FailNow()
}

// Fatalf writes a formatted error message to the log and fail the text immediately.
func (t *T) Fatalf(format string, args ...any) {
	t.log.Errorf(format, args...)
	t.FailNow()
}

// Skip is equivalent to Log followed by SkipNow.
func (t *T) Skip(msg string) {
	t.log.Info(msg)
	t.SkipNow()
}

// Skipf is equivalent to Logf followed by SkipNow.
func (t *T) Skipf(format string, args ...any) {
	t.log.Infof(format, args...)
	t.SkipNow()
}
