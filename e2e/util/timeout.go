// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"time"
)

/* Typical e2e run from the CI:

--- PASS: TestDR (7.41s)
    --- PASS: TestDR/subscr-deploy-rbd-busybox (529.69s)
        --- PASS: TestDR/subscr-deploy-rbd-busybox/Deploy (35.94s)
        --- PASS: TestDR/subscr-deploy-rbd-busybox/Enable (95.94s)
        --- PASS: TestDR/subscr-deploy-rbd-busybox/Failover (211.88s)
        --- PASS: TestDR/subscr-deploy-rbd-busybox/Relocate (136.26s)
        --- PASS: TestDR/subscr-deploy-rbd-busybox/Disable (39.83s)
        --- PASS: TestDR/subscr-deploy-rbd-busybox/Undeploy (9.83s)
    --- PASS: TestDR/disapp-deploy-rbd-busybox (559.51s)
        --- PASS: TestDR/disapp-deploy-rbd-busybox/Deploy (3.18s)
        --- PASS: TestDR/disapp-deploy-rbd-busybox/Enable (91.22s)
        --- PASS: TestDR/disapp-deploy-rbd-busybox/Failover (274.42s)
        --- PASS: TestDR/disapp-deploy-rbd-busybox/Relocate (118.94s)
        --- PASS: TestDR/disapp-deploy-rbd-busybox/Disable (58.09s)
        --- PASS: TestDR/disapp-deploy-rbd-busybox/Undeploy (13.65s)
    --- PASS: TestDR/disapp-deploy-cephfs-busybox (745.69s)
        --- PASS: TestDR/disapp-deploy-cephfs-busybox/Deploy (3.12s)
        --- PASS: TestDR/disapp-deploy-cephfs-busybox/Enable (96.10s)
        --- PASS: TestDR/disapp-deploy-cephfs-busybox/Failover (269.83s)
        --- PASS: TestDR/disapp-deploy-cephfs-busybox/Relocate (305.47s)
        --- PASS: TestDR/disapp-deploy-cephfs-busybox/Disable (57.77s)
        --- PASS: TestDR/disapp-deploy-cephfs-busybox/Undeploy (13.39s)
    --- PASS: TestDR/subscr-deploy-cephfs-busybox (839.47s)
        --- PASS: TestDR/subscr-deploy-cephfs-busybox/Deploy (10.87s)
        --- PASS: TestDR/subscr-deploy-cephfs-busybox/Enable (156.18s)
        --- PASS: TestDR/subscr-deploy-cephfs-busybox/Failover (297.89s)
        --- PASS: TestDR/subscr-deploy-cephfs-busybox/Relocate (304.82s)
        --- PASS: TestDR/subscr-deploy-cephfs-busybox/Disable (56.29s)
        --- PASS: TestDR/subscr-deploy-cephfs-busybox/Undeploy (13.44s)
    --- PASS: TestDR/appset-deploy-rbd-busybox (950.11s)
        --- PASS: TestDR/appset-deploy-rbd-busybox/Deploy (35.48s)
        --- PASS: TestDR/appset-deploy-rbd-busybox/Enable (96.41s)
        --- PASS: TestDR/appset-deploy-rbd-busybox/Failover (393.43s)
        --- PASS: TestDR/appset-deploy-rbd-busybox/Relocate (365.22s)
        --- PASS: TestDR/appset-deploy-rbd-busybox/Disable (52.27s)
        --- PASS: TestDR/appset-deploy-rbd-busybox/Undeploy (7.29s)
    --- PASS: TestDR/appset-deploy-cephfs-busybox (1219.81s)
        --- PASS: TestDR/appset-deploy-cephfs-busybox/Deploy (45.51s)
        --- PASS: TestDR/appset-deploy-cephfs-busybox/Enable (96.48s)
        --- PASS: TestDR/appset-deploy-cephfs-busybox/Failover (388.60s)
        --- PASS: TestDR/appset-deploy-cephfs-busybox/Relocate (568.36s)
        --- PASS: TestDR/appset-deploy-cephfs-busybox/Disable (36.72s)
        --- PASS: TestDR/appset-deploy-cephfs-busybox/Undeploy (84.14s)
*/

const (
	DeployTimeout   = 5 * time.Minute
	UndeployTimeout = 5 * time.Minute
	EnableTimeout   = 5 * time.Minute
	DisableTimeout  = 5 * time.Minute
	PurgeTimeout    = 5 * time.Minute

	// With appset deployment may need 2m30s after failover and relocate.
	FailoverTimeout = 15 * time.Minute
	RelocateTimeout = 15 * time.Minute

	// Polling internal during wait.
	RetryInterval = 5 * time.Second
)

// Sleep pauses the current goroutine for at least the duration d. If the context was canceled or its deadline has
// exceeded it will return early with a context.Canceled or a context.DeadlineExceeded error.
func Sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
