// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"fmt"
	"time"

	subscriptionv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/v1"

	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

func waitSubscriptionPhase(
	ctx types.TestContext,
	namespace, name string,
	phase subscriptionv1.SubscriptionPhase,
) error {
	log := ctx.Logger()
	startTime := time.Now()

	for {
		sub, err := getSubscription(ctx, namespace, name)
		if err != nil {
			return err
		}

		currentPhase := sub.Status.Phase
		if currentPhase == phase {
			log.Debugf("Subscription \"%s/%s\" phase is %s in cluster %q", namespace, name, phase, ctx.Env().Hub.Name)

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("subscription %q status is not %q yet before timeout in cluster %q",
				name, phase, ctx.Env().Hub.Name)
		}

		if err := util.Sleep(ctx.Context(), util.RetryInterval); err != nil {
			return err
		}
	}
}

func WaitWorkloadHealth(ctx types.TestContext, cluster types.Cluster, namespace string) error {
	log := ctx.Logger()
	w := ctx.Workload()
	startTime := time.Now()

	for {
		err := w.Health(ctx, cluster, namespace)
		if err == nil {
			log.Debugf("Workload \"%s/%s\" is ready in cluster %q", namespace, w.GetAppName(), cluster.Name)

			return nil
		}

		if time.Since(startTime) > util.Timeout {
			return fmt.Errorf("workload %q is not ready yet before timeout of %v in cluster %q",
				w.GetName(), util.Timeout, cluster.Name)
		}

		if err := util.Sleep(ctx.Context(), util.RetryInterval); err != nil {
			return err
		}
	}
}
