// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

type ApplicationSet struct{}

// func (a *ApplicationSet) Init() {
// }

func (a ApplicationSet) Deploy(w workloads.Workload) error {
	util.Ctx.Log.Info("enter Deploy " + w.GetName() + "/Appset")

	return nil
}

func (a ApplicationSet) Undeploy(w workloads.Workload) error {
	util.Ctx.Log.Info("enter Undeploy " + w.GetName() + "/Appset")

	return nil
}

func (a ApplicationSet) GetName() string {
	return "Appset"
}
