//go:build !windows

// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import "syscall"

var runInBackground = syscall.SysProcAttr{
	Setpgid: true,
}
