// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package common

import (
	"strings"

	Recipe "github.com/ramendr/recipe/api/v1alpha1"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"
)

// ParseInverseOp parses the inverse operation string into hookName and opName.
// Supported formats:
//   - "hookName/opName" - uses the specified hookName and opName
//   - "/opName" - uses defaultHookName with the specified opName
//   - "opName" - uses defaultHookName with the specified opName
func ParseInverseOp(inverseOp, defaultHookName string) (hookName, opName string) {
	if inverseOp == "" {
		return "", ""
	}

	// Handle "/opName" format (same hook)
	if strings.HasPrefix(inverseOp, "/") {
		return defaultHookName, strings.TrimPrefix(inverseOp, "/")
	}

	// Handle "hookName/opName" or "opName" format
	const (
		maxParts      = 2
		twoPartFormat = 2
	)

	parts := strings.SplitN(inverseOp, "/", maxParts)
	if len(parts) == twoPartFormat {
		return parts[0], parts[1]
	}

	// Just "opName" - use default hook
	return defaultHookName, parts[0]
}

// GetHookSpecForInverseOp retrieves the hook specification for an inverse operation.
// It searches through the recipe hooks and returns the appropriate HookSpec.
func GetHookSpecForInverseOp(hooks []*Recipe.Hook, inverseOp, defaultHookName string) *kubeobjects.HookSpec {
	hookName, opName := ParseInverseOp(inverseOp, defaultHookName)
	if hookName == "" || opName == "" {
		return nil
	}

	// Find the hook by name
	for _, hook := range hooks {
		if hook.Name == hookName {
			return GetHookSpecFromRecipe(hook, opName)
		}
	}

	return nil
}

func ShouldInverseOpBeExecuted(inverseOp string, hookSpec *kubeobjects.HookSpec, err error) bool {
	return err != nil && inverseOp != "" && ShouldFailOnError(hookSpec)
}
