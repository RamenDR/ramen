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

func delaySetMinimum(result *ctrl.Result) {
	result.RequeueAfter = time.Nanosecond
}

func calibrateRequeueAfterTime(result *ctrl.Result, delay time.Duration, log logr.Logger) {
	// If the next reconcile is before the next capture start time, don't calibrate the requeue time.
	// We will skip the capture in that reconcile and let the reconcile do other work.
	if result.RequeueAfter > 0 && result.RequeueAfter <= delay {
		return
	}

	log.Info("setting requeue after to remaining time in the capture interval", "requeuing after", delay)

	result.RequeueAfter = delay
}
