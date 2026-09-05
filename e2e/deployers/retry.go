// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"errors"
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
	start := time.Now()

	log.Debugf("Waiting until subscription \"%s/%s\" reach phase %q in cluster %q",
		namespace, name, phase, ctx.Env().Hub.Name)

	for {
		sub, err := getSubscription(ctx, namespace, name)
		if err != nil {
			return err
		}

		currentPhase := sub.Status.Phase
		if currentPhase == phase {
			elapsed := time.Since(start)
			log.Debugf("Subscription \"%s/%s\" phase is %s in cluster %q in %.3f seconds",
				namespace, name, phase, ctx.Env().Hub.Name, elapsed.Seconds())

			return nil
		}

		if err := util.Sleep(ctx.Context(), util.RetryInterval); err != nil {
			return fmt.Errorf("subscription %q status is not %q in cluster %q: %w",
				name, phase, ctx.Env().Hub.Name, err)
		}
	}
}

func WaitWorkloadHealth(ctx types.TestContext, cluster *types.Cluster) error {
	log := ctx.Logger()
	w := ctx.Workload()
	start := time.Now()

	log.Debugf("Waiting until workload \"%s/%s\" is healthy in cluster %q",
		ctx.AppNamespace(), w.GetAppName(), cluster.Name)

	for {
		err := w.Health(ctx, cluster)
		if err == nil {
			elapsed := time.Since(start)
			log.Debugf("Workload \"%s/%s\" is healthy in cluster %q in %.3f seconds",
				ctx.AppNamespace(), w.GetAppName(), cluster.Name, elapsed.Seconds())

			return nil
		}

		if errors.Is(err, util.ErrUnrecoverable) {
			return err
		}

		if err := util.Sleep(ctx.Context(), util.RetryInterval); err != nil {
			return fmt.Errorf("workload \"%s/%s\" is not healthy in cluster %q: %w",
				ctx.AppNamespace(), w.GetAppName(), cluster.Name, err)
		}
	}
}

func WaitWorkloadAbsent(ctx types.TestContext, cluster *types.Cluster) error {
	log := ctx.Logger()
	w := ctx.Workload()
	start := time.Now()

	log.Debugf("Waiting until workload \"%s/%s\" is removed from cluster %q",
		ctx.AppNamespace(), w.GetAppName(), cluster.Name)

	for {
		absent, err := workloadAbsentOnCluster(ctx, cluster)
		if err != nil {
			return err
		}

		if absent {
			elapsed := time.Since(start)
			log.Debugf("Workload \"%s/%s\" removed from cluster %q in %.3f seconds",
				ctx.AppNamespace(), w.GetAppName(), cluster.Name, elapsed.Seconds())

			return nil
		}

		if err := util.Sleep(ctx.Context(), util.RetryInterval); err != nil {
			return fmt.Errorf("workload \"%s/%s\" still present on cluster %q: %w",
				ctx.AppNamespace(), w.GetAppName(), cluster.Name, err)
		}
	}
}

func workloadAbsentOnCluster(ctx types.TestContext, cluster *types.Cluster) (bool, error) {
	statuses, err := ctx.Workload().Status(ctx)
	if err != nil {
		return false, err
	}

	for _, status := range statuses {
		if status.ClusterName == cluster.Name {
			return false, nil
		}
	}

	return true, nil
}
