// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"time"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

func delayResetIfRequeueTrue(result *ctrl.Result, _ logr.Logger) {
	if result.Requeue {
		result.RequeueAfter = 0
	}
}

func delaySetMinimum(result *ctrl.Result) {
	result.RequeueAfter = time.Nanosecond
}

func delaySetIfLess(result *ctrl.Result, delay time.Duration, _ logr.Logger) {
	if result.RequeueAfter > 0 && result.RequeueAfter <= delay {
		return
	}

	result.RequeueAfter = delay
}
