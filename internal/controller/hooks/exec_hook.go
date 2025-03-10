// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"github.com/go-logr/logr"
	"github.com/ramendr/ramen/internal/controller/kubeobjects"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ExecHook struct {
	Hook *kubeobjects.HookSpec
}

func (e ExecHook) Execute(client client.Client, log logr.Logger) error {
	return nil
}
