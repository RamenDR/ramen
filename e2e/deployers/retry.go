// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"fmt"

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

	log.Debugf("Waiting until subscription \"%s/%s\" reach phase %q in cluster %q",
		namespace, name, phase, ctx.Env().Hub.Name)

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

		if err := util.Sleep(ctx.Context(), util.RetryInterval); err != nil {
			return fmt.Errorf("subscription %q status is not %q in cluster %q: %w",
				name, phase, ctx.Env().Hub.Name, err)
		}
	}
}

func WaitWorkloadHealth(ctx types.TestContext, cluster types.Cluster, namespace string) error {
	log := ctx.Logger()
	w := ctx.Workload()

	log.Debugf("Waiting until workload \"%s/%s\" is healthy in cluster %q", namespace, w.GetAppName(), cluster.Name)

	for {
		err := w.Health(ctx, cluster, namespace)
		if err == nil {
			log.Debugf("Workload \"%s/%s\" is healthy in cluster %q", namespace, w.GetAppName(), cluster.Name)

			return nil
		}

		if err := util.Sleep(ctx.Context(), util.RetryInterval); err != nil {
			return fmt.Errorf("workload \"%s/%s\" is not healthy in cluster %q: %w",
				namespace, w.GetAppName(), cluster.Name, err)
		}
	}
}
