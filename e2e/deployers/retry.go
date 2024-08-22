// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"fmt"
	"time"

	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
	subscriptionv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const FiveSecondsDuration = 5 * time.Second

func waitSubscriptionPhase(namespace, name string, phase subscriptionv1.SubscriptionPhase) error {
	// sleep to wait for subscription is processed
	time.Sleep(FiveSecondsDuration)

	startTime := time.Now()

	for {
		sub, err := getSubscription(util.Ctx.Hub.CtrlClient, namespace, name)
		if err != nil {
			return err
		}

		currentPhase := sub.Status.Phase
		if currentPhase == phase {
			util.Ctx.Log.Info(fmt.Sprintf("subscription %s phase is %s", name, phase))

			return nil
		}

		if time.Since(startTime) > time.Second*time.Duration(util.Timeout) {
			return fmt.Errorf(fmt.Sprintf("subscription %s status is not %s yet before timeout", name, phase))
		}

		time.Sleep(time.Second * time.Duration(util.TimeInterval))
	}
}

func WaitWorkloadHealth(client client.Client, namespace string, w workloads.Workload) error {
	time.Sleep(FiveSecondsDuration)

	startTime := time.Now()

	for {
		err := w.Health(client, namespace)
		if err == nil {
			util.Ctx.Log.Info(fmt.Sprintf("workload %s is ready", w.GetName()))

			return nil
		}

		if time.Since(startTime) > time.Second*time.Duration(util.Timeout) {
			util.Ctx.Log.Info(err.Error())

			return fmt.Errorf(fmt.Sprintf("workload %s is not ready yet before timeout of %v",
				w.GetName(), util.Timeout))
		}

		time.Sleep(time.Second * time.Duration(util.TimeInterval))
	}
}
