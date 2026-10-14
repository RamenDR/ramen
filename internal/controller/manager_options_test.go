// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"strings"
	"testing"

	configv1alpha1 "k8s.io/component-base/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"

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

func TestDeprecatedManagerOptionWarningsNone(t *testing.T) {
	leaderElect := true
	cfg := &ramendrv1alpha1.RamenConfig{
		Health:  ramendrv1alpha1.ControllerHealth{HealthProbeBindAddress: ":8081"},
		Metrics: ramendrv1alpha1.ControllerMetrics{BindAddress: "0.0.0.0:9289"},
		LeaderElection: &configv1alpha1.LeaderElectionConfiguration{
			LeaderElect:  &leaderElect,
			ResourceName: "hub.ramendr.openshift.io",
		},
	}
	options := &ctrl.Options{
		HealthProbeBindAddress: ":8081",
		Metrics:                controllers.MetricsServerOptions("0.0.0.0:9289"),
		LeaderElection:         true,
		LeaderElectionID:       "hub.ramendr.openshift.io",
	}

	warnings := controllers.DeprecatedManagerOptionWarnings(cfg, options)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestDeprecatedManagerOptionWarningsEmptyConfig(t *testing.T) {
	cfg := &ramendrv1alpha1.RamenConfig{}
	options := &ctrl.Options{
		HealthProbeBindAddress: ":8081",
		Metrics:                controllers.MetricsServerOptions("0.0.0.0:9289"),
		LeaderElection:         true,
		LeaderElectionID:       "hub.ramendr.openshift.io",
	}

	warnings := controllers.DeprecatedManagerOptionWarnings(cfg, options)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for unset config fields, got %v", warnings)
	}
}

func TestDeprecatedManagerOptionWarningsStaleValues(t *testing.T) {
	leaderElect := false
	cfg := &ramendrv1alpha1.RamenConfig{
		Health:  ramendrv1alpha1.ControllerHealth{HealthProbeBindAddress: ":8082"},
		Metrics: ramendrv1alpha1.ControllerMetrics{BindAddress: ":8443"},
		LeaderElection: &configv1alpha1.LeaderElectionConfiguration{
			LeaderElect:  &leaderElect,
			ResourceName: "stale.ramendr.openshift.io",
		},
	}
	options := &ctrl.Options{
		HealthProbeBindAddress: ":8081",
		Metrics:                controllers.MetricsServerOptions("0.0.0.0:9289"),
		LeaderElection:         true,
		LeaderElectionID:       "hub.ramendr.openshift.io",
	}

	warnings := controllers.DeprecatedManagerOptionWarnings(cfg, options)
	if len(warnings) != 4 {
		t.Fatalf("expected 4 warnings, got %d: %v", len(warnings), warnings)
	}

	for _, substring := range []string{
		"health.healthProbeBindAddress",
		"metrics.bindAddress",
		"leaderElection.leaderElect",
		"leaderElection.resourceName",
	} {
		found := false

		for _, w := range warnings {
			if strings.Contains(w, substring) {
				found = true
			}
		}

		if !found {
			t.Errorf("expected a warning mentioning %q, got %v", substring, warnings)
		}
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
