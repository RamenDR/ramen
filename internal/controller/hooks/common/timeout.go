// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package common

import "github.com/ramendr/ramen/internal/controller/kubeobjects"

const DefaultTimeoutValue = 300

// GetHookTimeout returns the timeout value for any hook type.
// Priority: operation-specific timeout > hook-level timeout > default (300s)
func GetHookTimeout(hook *kubeobjects.HookSpec) int {
	// Check operation-specific timeout based on populated fields
	// Priority: operation-specific > hook-level > default (300s)

	// Check Job timeout
	if hook.Job.Timeout != 0 {
		return hook.Job.Timeout
	}

	// Check Op (exec) timeout
	if hook.Op.Timeout != 0 {
		return hook.Op.Timeout
	}

	// Check Chk (check) timeout
	if hook.Chk.Timeout != 0 {
		return hook.Chk.Timeout
	}

	// Check hook-level timeout
	if hook.Timeout != 0 {
		return hook.Timeout
	}

	// Return default
	return DefaultTimeoutValue
}
