// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"testing"

	configv1alpha1 "k8s.io/component-base/config/v1alpha1"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

func TestObjectsToDeployWithoutLeaderElection(t *testing.T) {
	// A hub ConfigMap without the deprecated leaderElection field must not
	// panic the dr-cluster deploy path.
	cfg := &ramendrv1alpha1.RamenConfig{}

	objects, err := objectsToDeploy(cfg)
	if err != nil {
		t.Fatalf("objectsToDeploy failed: %v", err)
	}

	if len(objects) == 0 {
		t.Error("expected objects to deploy")
	}
}

func TestObjectsToDeployKeepsHubLeaderElection(t *testing.T) {
	// The hub's in-memory config must not be mutated when deriving the
	// dr-cluster config: the copied config shares the LeaderElection pointer.
	cfg := &ramendrv1alpha1.RamenConfig{
		LeaderElection: &configv1alpha1.LeaderElectionConfiguration{
			ResourceName: HubLeaderElectionResourceName,
		},
	}

	if _, err := objectsToDeploy(cfg); err != nil {
		t.Fatalf("objectsToDeploy failed: %v", err)
	}

	if cfg.LeaderElection.ResourceName != HubLeaderElectionResourceName {
		t.Errorf("hub config leader election resource name mutated to %q",
			cfg.LeaderElection.ResourceName)
	}
}
