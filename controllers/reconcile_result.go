/*
Copyright 2022 The RamenDR authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
