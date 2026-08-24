// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"testing"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	controllers "github.com/ramendr/ramen/internal/controller"
)

func TestMetricsServerOptionsDisabled(t *testing.T) {
	options := controllers.MetricsServerOptions("0")

	if options.BindAddress != "0" {
		t.Errorf("expected BindAddress \"0\", got %q", options.BindAddress)
	}

	if options.SecureServing {
		t.Error("expected SecureServing disabled when metrics are disabled")
	}

	if options.FilterProvider != nil {
		t.Error("expected no FilterProvider when metrics are disabled")
	}
}

func TestMetricsServerOptionsEnabled(t *testing.T) {
	options := controllers.MetricsServerOptions("0.0.0.0:9289")

	if options.BindAddress != "0.0.0.0:9289" {
		t.Errorf("expected BindAddress \"0.0.0.0:9289\", got %q", options.BindAddress)
	}

	if !options.SecureServing {
		t.Error("expected SecureServing enabled")
	}

	if options.CertDir != "/etc/metrics-certs" {
		t.Errorf("expected CertDir \"/etc/metrics-certs\", got %q", options.CertDir)
	}

	if options.FilterProvider == nil {
		t.Error("expected FilterProvider to be set")
	}
}

func TestLeaderElectionResourceName(t *testing.T) {
	name := controllers.LeaderElectionResourceName(ramendrv1alpha1.DRHubType)
	if name != "hub.ramendr.openshift.io" {
		t.Errorf("expected hub leader election resource name, got %q", name)
	}

	name = controllers.LeaderElectionResourceName(ramendrv1alpha1.DRClusterType)
	if name != "dr-cluster.ramendr.openshift.io" {
		t.Errorf("expected dr-cluster leader election resource name, got %q", name)
	}
}
