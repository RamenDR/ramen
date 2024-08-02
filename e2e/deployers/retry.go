// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"fmt"
	"time"

	"github.com/ramendr/ramen/e2e/util"
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

func waitDeploymentReady(client client.Client, namespace, name string) error {
	time.Sleep(FiveSecondsDuration)

	startTime := time.Now()

	for {
		deploy, err := getDeployment(client, namespace, name)
		if err != nil {
			return err
		}

		if deploy.Status.Replicas == deploy.Status.ReadyReplicas {
			util.Ctx.Log.Info(fmt.Sprintf("deployment %s is ready", name))

			return nil
		}

		if time.Since(startTime) > time.Second*time.Duration(util.Timeout) {
			return fmt.Errorf(fmt.Sprintf("deployment %s is not ready yet before timeout of %v",
				name, util.Timeout))
		}

		time.Sleep(time.Second * time.Duration(util.TimeInterval))
	}
}
