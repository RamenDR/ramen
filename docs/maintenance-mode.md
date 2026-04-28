<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Maintenance Mode

## Why it is needed

Some storage backends require preparation before Ramen can proceed
with a DR operation. For example, a replication daemon may need to be
stopped before volumes are promoted, or a storage system may need to
flush caches or fence access to avoid data corruption.

Maintenance mode is a storage-agnostic coordination mechanism: Ramen
signals "I'm about to failover", the storage backend prepares and
reports completion, and only then does Ramen proceed. This allows
storage providers to integrate custom logic into Ramen's DR workflows
without Ramen needing to know storage-specific implementation details.

See [RBD mirroring](#rbd-mirroring) for the concrete example that
motivated this feature, and [MaintenanceMode](crds/maintenance-mode.md)
for the CRD reference.

## How it works

1. **Storage provider opts in:** Labels its VolumeReplicationClass
   with `ramendr.openshift.io/maintenancemodes: Failover`.

1. **VRG detects maintenance mode requirement:** The VRG controller
   on the managed cluster reads this label and extracts
   `storageProvisioner` and `targetID` from the storage class labels.
   It stores the mode in
   `ProtectedPVC.StorageIdentifiers.ReplicationID.Modes`. This is
   propagated to the hub via ManagedClusterView.

1. **DRPC initiates failover:** It checks `ReplicationID.Modes` for
   each protected PVC. If any require `Failover` mode, DRPC enters
   `ProgressionWaitForStorageMaintenanceActivation` and waits.

1. **Ramen creates MaintenanceMode:** The DRCluster controller creates
   a `MaintenanceMode` CR on the failover cluster via ManifestWork and
   monitors its status via ManagedClusterView.

1. **Storage provider reconciles:** The storage provider's controller
   on the managed cluster reconciles the `MaintenanceMode` CR matching
   its `storageProvisioner`, identifies the specific backend via
   `targetID`, and performs the necessary storage operations (e.g.,
   quiesce, flush, fence). It updates status to `Progressing` then
   sets `state: Completed` with `FailoverActivated: True`.

1. **Ramen proceeds:** DRCluster propagates the status to
   `DRCluster.Status.MaintenanceModes`. DRPC sees the activation is
   complete and proceeds with failover.

1. **Cleanup:** After all failover DRPCs targeting the cluster are
   fully available, the DRCluster controller deletes the ManifestWork,
   removing the `MaintenanceMode` CR from the managed cluster.

## Dependencies

Ramen only creates and monitors the `MaintenanceMode` CR -- it does
**not** reconcile it. A separate controller provided by the storage
vendor must watch for `MaintenanceMode` resources and act on them.

For Ceph RBD, see [RBD mirroring](#rbd-mirroring) for the specific
controller that handles this.

## Usage

MaintenanceMode resources are created and deleted automatically by
Ramen. Users should not create them manually.

### Monitoring during failover

```bash
# On managed cluster: watch MaintenanceMode lifecycle
kubectl get maintenancemode -w

# On hub: check DRCluster maintenance mode status
kubectl get drcluster <cluster> -o jsonpath='{.status.maintenanceModes}' | jq

# On hub: check DRPC progression
kubectl get drpc <name> -n <ns> -o jsonpath='{.status.progression}'
```

## Troubleshooting

### Failover stuck at maintenance mode

If DRPC shows `progression: WaitForStorageMaintenanceActivation`, the
storage backend has not completed its preparation.

1. Check if the MaintenanceMode CR exists on the managed cluster:

   ```bash
   kubectl get maintenancemode
   ```

1. If it exists but has no status, the storage provider controller is
   not reconciling it. Check that the controller is running:

   ```bash
   kubectl get pods -n <storage-namespace> | grep operator
   kubectl logs -n <storage-namespace> <controller-pod> | grep -i maintenance
   ```

1. If the MaintenanceMode CR does not exist, check that the
   VolumeReplicationClass has the required label:

   ```bash
   kubectl get volumereplicationclass -o yaml | grep maintenancemodes
   ```

### MaintenanceMode stuck in progressing

**Symptom:** `state: Progressing` for extended period, failover not completing.

**Common causes:**

- Storage provider controller not watching MaintenanceMode
- TargetID doesn't match any managed storage backend
- Storage backend unreachable or unhealthy

**Solution:** Check storage provider integration and backend health.

### MaintenanceMode in error state

**Check conditions:**

```bash
kubectl get maintenancemode <name> -o jsonpath='{.status.conditions}' | jq
```

**Review message:**

```bash
kubectl get maintenancemode <name> -o jsonpath='{.status.conditions[?(@.type=="FailoverActivated")].message}'
```

**Solution:** Address the specific error reported in the
condition message. Common issues:

- Storage backend connectivity problems
- Insufficient permissions
- Backend already in maintenance mode

### VRG waiting for maintenance mode

**Symptom:** VRG status shows waiting for maintenance mode completion.

**Check VRG events:**

```bash
kubectl describe vrg <vrg-name> -n <namespace>
```

**Verify MaintenanceMode exists:**

```bash
kubectl get maintenancemode | grep <storage-provisioner>
```

**Solution:** If MaintenanceMode doesn't exist, check if
StorageClass has maintenance mode labels.

### MaintenanceMode not created

**Symptom:** Failover proceeds without creating MaintenanceMode when expected.

**Check StorageClass labels:**

```bash
kubectl get sc <storage-class> -o yaml | grep maintenancemodes
```

**Expected label:**

```yaml
labels:
  ramendr.openshift.io/maintenancemodes: Failover
```

**Solution:** Add maintenance mode label if storage backend requires it.

## Storage provider integration

To integrate your storage backend with Ramen's maintenance mode:

### Label your VolumeReplicationClass

```yaml
apiVersion: replication.storage.openshift.io/v1alpha1
kind: VolumeReplicationClass
metadata:
  name: rbd-replication
  labels:
    ramendr.openshift.io/replicationid: your-replication-id
    ramendr.openshift.io/maintenancemodes: Failover
spec:
  provisioner: your.csi.driver
```

The label can also be placed on the StorageClass (using
`ramendr.openshift.io/storageid`), but only the
VolumeReplicationClass label is checked during the failover
prerequisite check.

### Implement a controller that reconciles MaintenanceMode

```go
func (r *YourReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    mmode := &ramendrv1alpha1.MaintenanceMode{}
    if err := r.Get(ctx, req.NamespacedName, mmode); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    if mmode.Spec.StorageProvisioner != "your.csi.driver" {
        return ctrl.Result{}, nil
    }

    for _, mode := range mmode.Spec.Modes {
        switch mode {
        case ramendrv1alpha1.MModeFailover:
            if err := r.prepareForFailover(mmode.Spec.TargetID); err != nil {
                mmode.Status.State = ramendrv1alpha1.MModeStateError
                meta.SetStatusCondition(&mmode.Status.Conditions, metav1.Condition{
                    Type:   string(ramendrv1alpha1.MModeConditionFailoverActivated),
                    Status: metav1.ConditionFalse,
                    Reason: "Error",
                    Message: err.Error(),
                })
                return ctrl.Result{}, r.Status().Update(ctx, mmode)
            }
        }
    }

    mmode.Status.State = ramendrv1alpha1.MModeStateCompleted
    mmode.Status.ObservedGeneration = mmode.Generation
    meta.SetStatusCondition(&mmode.Status.Conditions, metav1.Condition{
        Type:   string(ramendrv1alpha1.MModeConditionFailoverActivated),
        Status: metav1.ConditionTrue,
        Reason: "Completed",
    })
    return ctrl.Result{}, r.Status().Update(ctx, mmode)
}
```

Ramen checks both `state: Completed` and that `observedGeneration`
matches `metadata.generation` before proceeding. Make sure to set
both.

### Best practices

1. **Don't create MaintenanceMode manually** - Let VRG controller manage them

1. **Update status promptly:**

    - Set `state: Progressing` immediately
    - Update to `Completed` as soon as maintenance actions finish
    - Use descriptive error messages in conditions

1. **Handle multiple maintenance modes:**

    - One MaintenanceMode per storage provisioner + targetID combination
    - Support concurrent maintenance operations if applicable

1. **Implement timeouts:**

    - Storage providers should timeout long-running operations
    - Report errors via conditions with clear messages

1. **Clean up resources:**
    - Ramen deletes MaintenanceMode after DR operation
    - Storage providers should clean up any backend state

## RBD mirroring

RBD mirroring was the motivation for adding maintenance mode. During
failover, the rbd-mirror daemon on the failover cluster must be stopped
before volumes are promoted. If the daemon is still running during a
forced promote, the operation can get stuck, crash, or cause
split-brain. This is a long-standing limitation in Ceph's rbd-mirror:

- [Ceph #70520](https://tracker.ceph.com/issues/70520) -
  `rbd mirror image promote --force` gets stuck or crashes when a
  mirror snapshot is being synced. The Ceph maintainer's recommended
  workaround: "kill -9 rbd-mirror daemon on the secondary cluster
  before issuing rbd mirror image promote --force".
- [Ceph #18963](https://tracker.ceph.com/issues/18963) - Forced
  failover deadlocks when the remote peer is unreachable. The daemon
  gets stuck trying to stop image replayers.
- [Ceph #36659](https://tracker.ceph.com/issues/36659) - Forced
  promotion hangs after killing the remote cluster. The daemon cannot
  shut down because it cannot update status on the unresponsive peer.
- [Ceph #53460](https://tracker.ceph.com/issues/53460) - Split-brain
  after failover if rbd-mirror was not running during
  demotion/promotion. The daemon misses journal tags it needs to detect
  the state change.

MaintenanceMode automates the "kill rbd-mirror before promote"
workaround. When Ramen creates a `MaintenanceMode` CR with `Failover`
mode, the storage provider controller scales down the rbd-mirror
deployment, then reports completion so Ramen can safely proceed.

### Downstream (ODF)

In ODF, this is handled by
[ocs-client-operator](https://github.com/red-hat-storage/ocs-client-operator)
(`maintenancemode_controller.go`).

### Upstream

**There is no upstream controller that reconciles MaintenanceMode.**
Neither csi-addons nor upstream rook implement it. This means that
upstream users have no protection against the rbd-mirror issues
described above -- failover will proceed without stopping the
rbd-mirror daemon.

In the upstream test environment (drenv), maintenance mode is not
enabled because there is no controller to reconcile the CR
([issue #2432](https://github.com/RamenDR/ramen/issues/2432)).

## Development history

The feature was introduced in
[PR #819](https://github.com/RamenDR/ramen/pull/819) by Shyamsundar
Ranganathan to address the class of rbd-mirror problems described in
[RBD mirroring](#rbd-mirroring). Open tech debt is tracked in
[issue #835](https://github.com/RamenDR/ramen/issues/835).

### Original implementation

- `64ff4e99` - Initial scaffold of the MaintenanceMode CRD.
- `e9af5ea2` - Added storage identifiers per protected PVC in VRG, so
  Ramen knows which provisioner/target needs maintenance.
- `bb3cbfdd` / `5e0ecb31` - Added DRCluster watcher so DRPC can
  trigger maintenance mode via DRCluster.
- `b844fef0` - Added DRPC watcher for triggering maintenance mode by
  DRCluster.
- `a9e88dfe` - Added MaintenanceMode processing logic in the DRCluster
  reconciler -- create, prune, and garbage collect modes.

### Bug fixes

Getting the lifecycle right proved non-trivial:

- `45c74f86` - Adding MaintenanceMode caused ProtectedPVC conditions
  to be overwritten
  ([issue #865](https://github.com/RamenDR/ramen/issues/865)).
- `2a6a5dab` - Race condition: Available condition was set True too
  early, causing maintenance mode deletion before all PVCs were
  promoted.
- [BZ#2251381](https://bugzilla.redhat.com/show_bug.cgi?id=2251381) /
  [PR #1172](https://github.com/RamenDR/ramen/pull/1172) - Unexpected
  maintenance mode activation when workloads are deleted after
  failover.
- [PR #2401](https://github.com/RamenDR/ramen/pull/2401) - Failover
  getting stuck because maintenance mode was removed while VRG
  promotion was still failing, causing RBD mirroring to resume
  prematurely.
- `484a43b7` - A stuck failover for one storage backend was blocking
  maintenance mode cleanup for unrelated backends, freezing sync for
  uninvolved applications.
- [PR #2467](https://github.com/RamenDR/ramen/pull/2467) - Enabled
  maintenance mode in the test environment by adding the label to
  VolumeReplicationClass
  ([issue #2432](https://github.com/RamenDR/ramen/issues/2432)).
