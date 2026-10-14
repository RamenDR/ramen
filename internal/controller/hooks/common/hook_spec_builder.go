// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package common

import (
	Recipe "github.com/ramendr/recipe/api/v1alpha1"

	"github.com/ramendr/ramen/internal/controller/kubeobjects"
)

// GetHookSpecFromRecipe creates a HookSpec from a Recipe hook and operation name.
func GetHookSpecFromRecipe(hook *Recipe.Hook, opName string) *kubeobjects.HookSpec {
	switch hook.Type {
	case "exec":
		return GetExecHookSpec(hook, opName)
	case "check":
		return GetCheckHookSpec(hook, opName)
	case "scale":
		return GetScaleHookSpec(hook, opName)
	case "job":
		return GetJobHookSpec(hook, opName)
	default:
		return nil
	}
}

// GetCheckHookSpec creates a HookSpec for a check type hook
func GetCheckHookSpec(hook *Recipe.Hook, opName string) *kubeobjects.HookSpec {
	for _, chk := range hook.Chks {
		if chk.Name == opName {
			return &kubeobjects.HookSpec{
				Name:                 hook.Name,
				Namespace:            hook.Namespace,
				Type:                 hook.Type,
				SelectResource:       hook.SelectResource,
				LabelSelector:        hook.LabelSelector,
				NameSelector:         hook.NameSelector,
				Timeout:              hook.Timeout,
				OnError:              hook.OnError,
				SkipHookIfNotPresent: hook.SkipHookIfNotPresent,
				Chk: kubeobjects.Check{
					Name:      opName,
					Condition: chk.Condition,
					Timeout:   chk.Timeout,
					OnError:   chk.OnError,
				},
				Essential: hook.Essential,
			}
		}
	}

	return nil
}

// GetExecHookSpec creates a HookSpec for an exec type hook
func GetExecHookSpec(hook *Recipe.Hook, opName string) *kubeobjects.HookSpec {
	for _, op := range hook.Ops {
		if op.Name == opName {
			return &kubeobjects.HookSpec{
				Name:           hook.Name,
				Namespace:      hook.Namespace,
				Type:           hook.Type,
				Timeout:        hook.Timeout,
				OnError:        hook.OnError,
				SelectResource: hook.SelectResource,
				LabelSelector:  hook.LabelSelector,
				NameSelector:   hook.NameSelector,
				SinglePodOnly:  hook.SinglePodOnly,
				Op: kubeobjects.Operation{
					Name:      opName,
					Container: op.Container,
					Command:   op.Command,
					InverseOp: op.InverseOp,
					Timeout:   op.Timeout,
					OnError:   op.OnError,
				},
				Essential: hook.Essential,
			}
		}
	}

	return nil
}

// GetScaleHookSpec creates a HookSpec for a scale type hook
func GetScaleHookSpec(hook *Recipe.Hook, opName string) *kubeobjects.HookSpec {
	return &kubeobjects.HookSpec{
		Name:           hook.Name,
		Namespace:      hook.Namespace,
		Type:           hook.Type,
		SelectResource: hook.SelectResource,
		LabelSelector:  hook.LabelSelector,
		NameSelector:   hook.NameSelector,
		Essential:      hook.Essential,
		Timeout:        hook.Timeout,
		OnError:        hook.OnError,
		Scale: kubeobjects.ScaleSpec{
			Operation: opName,
		},
	}
}

// GetJobHookSpec creates a HookSpec for a job type hook
func GetJobHookSpec(hook *Recipe.Hook, opName string) *kubeobjects.HookSpec {
	for _, job := range hook.Jobs {
		if job.Name == opName {
			return &kubeobjects.HookSpec{
				Name:      hook.Name,
				Namespace: hook.Namespace,
				Type:      hook.Type,
				Timeout:   hook.Timeout,
				OnError:   hook.OnError,
				Essential: hook.Essential,
				Job: kubeobjects.JobSpec{
					Name:        opName,
					Timeout:     job.Timeout,
					OnError:     job.OnError,
					ForceCreate: job.ForceCreate,
					InverseOp:   job.InverseOp,
				},
			}
		}
	}

	return nil
}
