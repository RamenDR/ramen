// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"fmt"
	"time"

	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
	subscriptionv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func waitSubscriptionPhase(ctx types.Context, namespace, name string, phase subscriptionv1.SubscriptionPhase) error {
	log := ctx.Logger()
	startTime := time.Now()

	for {
		sub, err := getSubscription(util.Ctx.Hub.CtrlClient, namespace, name)
		if err != nil {
			return err
		}

		currentPhase := sub.Status.Phase
		if currentPhase == phase {
			log.Infof("Subscription phase is %s", phase)

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("subscription %q status is not %q yet before timeout", name, phase)
		}

		time.Sleep(util.RetryInterval)
	}
}

func WaitWorkloadHealth(ctx types.Context, client client.Client, namespace string) error {
	log := ctx.Logger()
	w := ctx.Workload()
	startTime := time.Now()

	for {
		err := w.Health(ctx, client, namespace)
		if err == nil {
			log.Info("Workload is ready")

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			log.Info(err.Error())

			return fmt.Errorf("workload %q is not ready yet before timeout of %v",
				w.GetName(), util.Timeout)
		}

		time.Sleep(util.RetryInterval)
	}
}
