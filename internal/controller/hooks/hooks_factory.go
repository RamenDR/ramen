// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"github.com/ramendr/ramen/internal/controller/util"
)

type HookContext struct {
	Hook           kubeobjects.HookSpec
	Client         client.Client
	Reader         client.Reader
	Scheme         *runtime.Scheme
	RecipeElements util.RecipeElements
}

// Hook interface will help in executing the hooks based on the types.
// Supported types are "check", "scale" and "exec". The implementor needs
// return the result which would be boolean and error if any.
type HookExecutor interface {
	Execute(log logr.Logger) error
}

// Based on the hook type, return the appropriate implementation of the hook.
func GetHookExecutor(ctx HookContext) (HookExecutor, error) {
	switch ctx.Hook.Type {
	case "check":
		return CheckHook{
			Hook:   &ctx.Hook,
			Reader: ctx.Reader,
		}, nil

	case "exec":
		return ExecHook{
			Hook:           &ctx.Hook,
			Reader:         ctx.Reader,
			Scheme:         ctx.Scheme,
			RecipeElements: ctx.RecipeElements,
		}, nil

	case "scale":
		return ScaleHook{
			Hook:   &ctx.Hook,
			Reader: ctx.Reader,
			Client: ctx.Client,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported hook type: %s", ctx.Hook.Type)
	}
}
