// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/dractions"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

// Deployers = {"Subscription", "AppSet", "Imperative"}
// Workloads = {"Deployment", "STS", "DaemonSet"}
// Classes   = {"rbd", "cephfs"}

const (
	GITPATH     = "workloads/deployment/base"
	GITREVISION = "main"
	APPNAME     = "busybox"
)

var (
	Workloads      = []workloads.Workload{}
	subscription   = &deployers.Subscription{}
	appset         = &deployers.ApplicationSet{}
	discoveredApps = &deployers.DiscoveredApps{}
	Deployers      = []deployers.Deployer{subscription, appset, discoveredApps}
)

func generateSuffix(storageClassName string) string {
	suffix := storageClassName

	if strings.ToLower(storageClassName) == "rook-ceph-block" {
		suffix = "rbd"
	}

	if strings.ToLower(storageClassName) == "rook-cephfs" {
		suffix = "cephfs"
	}

	return suffix
}

func generateWorkloads([]workloads.Workload) {
	pvcSpecs := util.GetPVCSpecs()
	for _, pvcSpec := range pvcSpecs {
		// add storageclass name to deployment name
		suffix := generateSuffix(pvcSpec.StorageClassName)
		deployment := &workloads.Deployment{
			Path:     GITPATH,
			Revision: GITREVISION,
			AppName:  APPNAME,
			Name:     fmt.Sprintf("Deployment-%s", suffix),
			PVCSpec:  pvcSpec,
		}
		Workloads = append(Workloads, deployment)
	}
}

func Exhaustive(t *testing.T) {
	t.Helper()
	t.Parallel()

	if err := util.EnsureChannel(); err != nil {
		t.Fatalf("failed to ensure channel: %v", err)
	}

	t.Cleanup(func() {
		if err := util.EnsureChannelDeleted(); err != nil {
			t.Fatalf("failed to ensure channel deleted: %v", err)
		}
	})

	generateWorkloads(Workloads)

	for _, workload := range Workloads {
		for _, deployer := range Deployers {
			// assign workload and deployer to a local variable to avoid parallel test issue
			// see https://go.dev/wiki/CommonMistakes
			w := workload
			d := deployer

			t.Run(w.GetName()+"-"+d.GetName(), func(t *testing.T) {
				t.Parallel()
				runTestFlow(t, w, d)
			})
		}
	}
}

func runTestFlow(t *testing.T, w workloads.Workload, d deployers.Deployer) {
	t.Helper()

	if !d.IsWorkloadSupported(w) {
		t.Skipf("Workload %s not supported by deployer %s, skip test", w.GetName(), d.GetName())
	}

	t.Run("Deploy", func(t *testing.T) {
		if err := d.Deploy(w); err != nil {
			t.Fatal("Deploy failed")
		}
	})

	t.Run("Enable", func(t *testing.T) {
		if err := dractions.EnableProtection(w, d); err != nil {
			t.Fatal("Enable failed")
		}
	})

	t.Run("Failover", func(t *testing.T) {
		if err := dractions.Failover(w, d); err != nil {
			t.Fatal("Failover failed")
		}
	})

	t.Run("Relocate", func(t *testing.T) {
		if err := dractions.Relocate(w, d); err != nil {
			t.Fatal("Relocate failed")
		}
	})

	t.Run("Disable", func(t *testing.T) {
		if err := dractions.DisableProtection(w, d); err != nil {
			t.Fatal("Disable failed")
		}
	})

	t.Run("Undeploy", func(t *testing.T) {
		if err := d.Undeploy(w); err != nil {
			t.Fatal("Undeploy failed")
		}
	})
}
