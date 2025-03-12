// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"fmt"

	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Hook interface will help in executing the hooks based on the types.
// Supported types are "check", "scale" and "exec". The implementor needs
// return the result which would be boolean and error if any.
type HookExecutor interface {
	Execute(client client.Client, log logr.Logger) error
}

// Based on the hook type, return the appropriate implementation of the hook.
func GetHookExecutor(hook kubeobjects.HookSpec) (HookExecutor, error) {
	switch hook.Type {
	case "check":
		return CheckHook{Hook: &hook}, nil
	case "exec":
		return ExecHook{Hook: &hook}, nil
	default:
		return nil, fmt.Errorf("unsupported hook type")
	}
}
