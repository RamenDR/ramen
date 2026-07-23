// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package common

import "github.com/ramendr/ramen/internal/controller/kubeobjects"

// ShouldFailOnError determines if a hook should fail on error.
// Priority: operation-specific onError > hook-level onError > default ("fail")
// Returns true if should fail, false if should continue
func ShouldFailOnError(hook *kubeobjects.HookSpec) bool {
	// Check operation-specific onError based on populated fields
	// Priority: operation-specific > hook-level > default (fail)

	// Check Job onError
	if hook.Job.OnError != "" {
		return hook.Job.OnError != "continue"
	}

	// Check Op (exec) onError
	if hook.Op.OnError != "" {
		return hook.Op.OnError != "continue"
	}

	// Check Chk (check) onError
	if hook.Chk.OnError != "" {
		return hook.Chk.OnError != "continue"
	}

	// Check hook-level onError
	if hook.OnError != "" {
		return hook.OnError != "continue"
	}

	// Default to fail
	return true
}
