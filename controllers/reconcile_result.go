// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"time"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

func delayResetIfRequeueTrue(result *ctrl.Result, log logr.Logger) {
	if result.Requeue {
		log.Info("Delay reset because requeue trumps delay", "delay", result.RequeueAfter)
		result.RequeueAfter = 0
	}
}

func delaySetIfLess(result *ctrl.Result, delay time.Duration, log logr.Logger) {
	if result.RequeueAfter > 0 && result.RequeueAfter <= delay {
		log.Info("Delay not set because current delay is more than zero and less than new delay",
			"current", result.RequeueAfter, "new", delay)

		return
	}

	log.Info("Delay set because current delay is zero or more than new delay",
		"current", result.RequeueAfter, "new", delay)

	result.RequeueAfter = delay
}
