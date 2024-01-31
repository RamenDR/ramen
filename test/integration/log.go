// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"github.com/go-logr/logr"
	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/ramendr/ramen/pkg/utils"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func ConfigureTestLogger() logr.Logger {
	opts := utils.ConfigureLogOptions()
	opts.DestWriter = ginkgo.GinkgoWriter

	testLogger := zap.New(zap.UseFlagOptions(&opts))
	ctrl.SetLogger(testLogger)

	return testLogger
}
