// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package deployers

import (
	"fmt"
	"time"

	"github.com/ramendr/ramen/e2e/util"
)

const FiveSecondsDuration = 5 * time.Second

func waitSubscriptionPhase(namespace, name, phase string) error {
	// sleep to wait for subscription is processed
	time.Sleep(FiveSecondsDuration)

	startTime := time.Now()

	for {
		sub, err := getSubscription(util.Ctx.Hub.CtrlClient, namespace, name)
		if err != nil {
			return err
		}

		currentPhase := string(sub.Status.Phase)
		if currentPhase == phase {
			util.Ctx.Log.Info("subscription " + name + " phase is " + phase)

			return nil
		}

		if time.Since(startTime) > time.Second*time.Duration(util.Timeout) {
			return fmt.Errorf(fmt.Sprintf("subscription "+name+" status is not %s yet before timeout of %v",
				phase, util.Timeout))
		}

		if currentPhase == "" {
			currentPhase = "empty"
		}

		util.Ctx.Log.Info(fmt.Sprintf("current subscription "+name+
			" phase is %s, expecting %s, retry in %v seconds", currentPhase, phase, util.TimeInterval))
		time.Sleep(time.Second * time.Duration(util.TimeInterval))
	}
}
