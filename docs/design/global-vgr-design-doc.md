# Global VolumeGroupReplication Design Document

## 1. Design

Some storage backends manage replication at the LUN or filesystem level,
where all applications sharing the same storage unit belong to a single
replication group and must transition state together. Global VGR builds on
top of Ramen's offloaded storage support to coordinate a shared VGR across
all VRGs in such a replication group, with multi-level consensus, label
propagation, status validation, watchers, and observability through
conditions, metrics, and alerts.

Protection is managed through a two-level hierarchy:

- **Application level**: Each application is protected by its own
  VolumeReplicationGroup (VRG).
- **Storage level**: Multiple VRGs that share the same replication group
  contribute to a single Global VGR in the Ramen operator namespace. Ramen
  acts as the consensus orchestrator, ensuring all VRGs agree before the
  shared VGR transitions state.

## 2. Prerequisites

Global VGR is layered on top of offloaded storage. The offloaded setup must
be fully achieved first (StorageClass labeled as offloaded with a unique
StorageID label, VolumeGroupReplicationClass with a unique
GroupReplicationID, external storage controller deployed, etc.).

On top of offloaded, the following is required for global:

- **VolumeGroupReplicationClass** on **both** peer clusters must
  additionally carry the label `ramendr.openshift.io/global = "true"` for
  the same GroupReplicationID. Both sides must have it; if only one side has
  the label, the PeerClass will not be marked as global.
- All PVCs in a VRG must share the same **GroupReplicationID**. If a VRG's
  PVCs map to different GroupReplicationIDs, global VGR processing will fail.

### Example: PeerClass in DRPolicy status

When the above prerequisites are met, the DRPolicy controller discovers the
peer relationship and populates a PeerClass entry:

```yaml
status:
  peerClasses:
    - clusterIDs: [cl-id-1, cl-id-2]
      storageClassName: sc-block
      storageID: [sid-1, sid-2]
      groupreplicationID: replication-group-01
      grouping: true
      offloaded: true
      global: true
```

## 3. Labels

| Label | Applied To | Value | Purpose |
|-------|-----------|-------|---------|
| `ramendr.openshift.io/global` | VolumeGroupReplicationClass | `"true"` | Marks a VGRClass as participating in global replication |
| `ramendr.openshift.io/groupreplicationid` | VolumeGroupReplicationClass | `<grID>` | Pre-existing; identifies the replication group |
| `ramendr.openshift.io/global-vgr` | VRG, DRPC | `global-<grID>` | Links resources to the shared global VGR; value doubles as the VGR resource name |

### Label Propagation

1. **Primary VRG** self-labels during PVC processing. It validates that PVCs
    map to a global PeerClass with the same GroupReplicationID, and labels
    itself with `ramendr.openshift.io/global-vgr = global-<grID>`.
1. **DRPC** reads the label from the primary VRG during reconciliation,
    copies it onto itself, and requeues.
1. **All VRGs** (including the secondary) receive the label from the DRPC.
    Whenever the DRPC creates or updates a VRG ManifestWork, it writes the
    `global-vgr` label into the VRG object.

The secondary VRG gets the label through this propagation, even though it has
no PVCs and no VGR. This ensures the secondary is ready for consensus checks
when failover or relocate happens.

## 4. VRG: Consensus, Creation, and Deletion

### 4.1 VRG-Level Consensus (State Consensus)

Before VGR group reconciliation as primary, the VRG checks that all peer VRGs
with the same `global-vgr` label on that cluster have the same desired
`replicationState`. If any peer has a different state, the VRG requeues
without performing VGR operations. The same consensus check applies on the
secondary path to prevent premature VGR state transitions when multiple VRGs
share a global VGR.

A `GlobalState` condition is set on the VRG status:

- True / ConsensusReached: all peers agree on the desired state.
- False / ConsensusNotReached: lists pending VRGs.

Example (consensus reached):

```yaml
- type: GlobalState
  status: "True"
  reason: ConsensusReached
  message: Consensus reached for state primary
```

Example (consensus not reached, app-2's VRG still secondary while app-1
wants primary):

```yaml
- type: GlobalState
  status: "False"
  reason: ConsensusNotReached
  message: "Waiting for VRGs to agree on state primary, pending: app-2-ns/app-2"
```

### 4.2 Creation

The global VGR is named `global-<grID>` and created in the Ramen operator
namespace. It uses a CG-only selector to capture PVCs from all VRGs in the
replication group, rather than a VRG-scoped selector, so that a single VGR
covers all applications in the group. It has no owner reference since no
single VRG owns it.

The global VGR is not backed up to S3 because it does not need to be
restored from backup during failover or relocate. Ramen creates or updates
the VGR directly on the target cluster when needed.

The VGR is only created when the cluster has PVCs to process. If multiple
VRGs attempt to create it simultaneously, "already exists" errors are handled
gracefully; the VRG requeues and picks up the existing resource.

Example:

```yaml
apiVersion: replication.storage.openshift.io/v1alpha1
kind: VolumeGroupReplication
metadata:
  name: global-replication-group-01
  namespace: ramen-system
  labels:
    ramendr.openshift.io/created-by-ramen: "true"
spec:
  replicationState: primary
  volumeGroupReplicationClassName: offloaded-vgrc
  autoResync: false
  external: true
  source:
    selector:
      matchLabels:
        ramendr.openshift.io/consistency-group: replication-group-01
```

### 4.3 Deletion: VGR Persists Until All VRGs Are Deleted

When a VRG with the `global-vgr` label enters its cleanup path:

1. It removes Ramen finalizers and annotations from its own PVCs.
1. If the VRG itself is **not** being deleted (e.g., transitioning from
    primary to secondary during failover/relocate) → the VGR is kept alive and
    updated, not deleted.
1. If the VRG **is** being deleted → it checks deletion consensus: lists
    all VRGs with the same label and verifies every one has a deletion
    timestamp.
1. If any peer VRG is still alive → skip VGR deletion, requeue.
1. Only when **all** VRGs are being deleted → delete the global VGR
    resource.

The VGR persists on a cluster once created. It is never destroyed when the
cluster transitions between primary and secondary; it simply gets updated.
It is only deleted when the entire replication group is being torn down.

#### Why the VGR is not deleted on secondary transition

For non-global VGR, each VRG owns its own VGR exclusively. After the VGR
reaches secondary status state, the owning VRG deletes it safely because no
other VRG references that VGR.

For global VGR, multiple VRGs share the same VGR and reconcile concurrently.
If the first VRG to observe `status.state=Secondary` deletes the VGR, a
concurrent peer VRG that already passed the `reconcileMissingVGR` guard (VGR
existed at that point) but has not yet reached `createOrUpdateVGR`, due to
`isPVCReadyForSecondary` API calls widening the window, would find the VGR
gone and recreate it with `spec.replicationState=Secondary`. The storage
provider must then again acknowledge the new VGR by setting
`status.state=Secondary` before the recreating VRG can proceed to delete it.
Keeping the VGR alive avoids this race.

### 4.4 VGR Watcher

When the global VGR status changes (e.g., state transition by the external
controller), all VRGs sharing that VGR need to react immediately. If a VGR
change is detected in the operator namespace, all VRGs with the matching
`global-vgr` label are enqueued for reconciliation.

### 4.5 Status Handling

Some external storage providers manage replication at the LUN level and use
`schedulingInterval: 0m` in the VolumeGroupReplicationClass. This signals
that replication is managed externally with no Ramen-driven RPO. Such
providers may not report `Completed`, `Validated`, `Degraded`, or `Resyncing`
conditions on the VGR status.

When `schedulingInterval` is `0m` and VGR `.status.state` matches the desired
`replicationState` (Primary or Secondary), Ramen short-circuits the normal
validation path. All four condition checks (`Completed`, `Validated`,
`Degraded`, `Resyncing`) are skipped, the transition is considered complete,
and PVC conditions are set directly. For non-zero scheduling intervals, the
normal validation path is used.

When the state matches, Ramen sets PVC conditions as follows:

- **DataReady**: set to True with reason Ready (Primary) or Replicated
  (Secondary) and message "PVC in the VolumeReplicationGroup is ready for
  use"
- **DataProtected**: set to True with reason DataProtected and message "PVC
  in the VolumeReplicationGroup is data protected by storage provider"

Example VRG and PVC conditions on **primary**:

```yaml
status:
  state: Primary
  conditions:
    - type: DataReady
      status: "True"
      reason: Ready
      message: PVC in the VolumeReplicationGroup is ready for use
    - type: DataProtected
      status: "True"
      reason: DataProtected
      message: PVC in the VolumeReplicationGroup is data protected by storage provider
    - type: GlobalState
      status: "True"
      reason: ConsensusReached
      message: Consensus reached for state primary
  protectedPVCs:
    - name: my-pvc
      conditions:
        - type: DataReady
          status: "True"
          reason: Ready
          message: PVC in the VolumeReplicationGroup is ready for use
        - type: DataProtected
          status: "True"
          reason: DataProtected
          message: PVC in the VolumeReplicationGroup is data protected by storage provider
```

Example VRG conditions on **secondary** (no PVCs, no VGR):

```yaml
status:
  state: Secondary
  protectedPVCs: []
  conditions:
    - type: DataReady
      status: "True"
      reason: Unused
      message: "No PVCs are protected using Volsync scheme. PVC protection as secondary is complete, or no PVCs needed protection using VolumeReplication scheme"
    - type: DataProtected
      status: "True"
      reason: Unused
      message: "No PVCs are protected using Volsync scheme. PVC protection as secondary is complete, or no PVCs needed protection using VolumeReplication scheme"
    - type: GlobalState
      status: "True"
      reason: ConsensusReached
      message: Consensus reached for state secondary
```

### 4.6 SchedulingInterval 0m

If the storage provider uses `schedulingInterval: 0m` in the
VolumeGroupReplicationClass and the corresponding DRPolicy, this indicates
that replication is managed entirely by the external storage provider and
Ramen does not drive RPO-based scheduling. It has the following
implications:

1. **VGR status validation**: The simplified status path described in 4.5
   only applies when `schedulingInterval` is `0m`. For non-zero intervals,
   the normal `Completed`/`Degraded`/`Resyncing` validation path is used.

1. **Protected condition**: Normally, the DRPC Protected condition fails
   when an async VRG reports zero `LastGroupSyncTime`. When
   `schedulingInterval` is `0m`, this check is skipped because
   `lastGroupSyncTime` is not expected from the external provider. Without
   this, the Protected condition would block indefinitely.

1. **Sync metrics**: DRPC sync/RPO metrics (`ramen_sync_duration_seconds`,
   etc.) are skipped when `DRPolicy.Spec.SchedulingInterval` is `0m`. RPO
   tracking is not applicable when the storage provider manages replication
   externally.

## 5. DRPC Consensus

### 5.1 Action Consensus Gate

Before a failover or relocate proceeds, all DRPCs sharing the same
`global-vgr` label must agree on:

- The **action**: Failover or Relocate.
- The **target cluster**: `spec.failoverCluster` for Failover,
  `spec.preferredCluster` for Relocate.

The consensus check runs after the DRPC status is set to "Initiating" but
before the actual switchover. It lists all DRPCs with the matching label and
verifies each peer has the same action and target cluster.

If any peer DRPC disagrees or hasn't been updated yet:

- Progression is set to `WaitOnGlobalAction`.
- `GlobalAction` condition is set to False with a message listing pending DRPCs.
- The DR action does **not** proceed. Applications remain running.

Once all DRPCs agree:

- `GlobalAction` condition is set to True.
- The failover or relocate proceeds normally.

Example (consensus reached):

```yaml
- type: GlobalAction
  status: "True"
  reason: ConsensusReached
  message: Consensus reached for action Failover
```

Example (consensus not reached, app-2's DRPC hasn't been updated yet):

```yaml
- type: GlobalAction
  status: "False"
  reason: ConsensusNotReached
  message: "Expected Failover to cluster-2, pending: app-2-ns/app-2"
```

If all DRPCs have `Action=Relocate` but one DRPC's `preferredCluster` is
changed to a different cluster (e.g., app-1 targets cluster-2 but app-2
targets cluster-3), consensus will never be reached. All DRPCs remain in
`WaitOnGlobalAction` until the mismatch is corrected. Applications continue
running on the current cluster.

### 5.2 DRPC Watcher

Without a watcher, if one DRPC in a global group is updated before others, it
enters exponential backoff while waiting. When the remaining DRPCs are
finally updated, the first may take minutes to notice.

The DRPC controller watches all DRPCs with a `global-vgr` label. It triggers on:

- **Update**: when action, failoverCluster, or preferredCluster changes.
- **Delete**: when a global DRPC is deleted.

When triggered, all peer DRPCs with the same label are enqueued for
reconciliation, ensuring instant notification.

### 5.3 Observability

- **Conditions**: `GlobalAction` (DRPC), whether all peer DRPCs agree on
  action and target cluster. `GlobalState` (VRG), whether all peer VRGs
  agree on replication state (checked on both primary and secondary paths).
- **Progression**: `WaitOnGlobalAction`, set on DRPC when consensus is
  blocked. Apps remain running.
- **Prometheus metric**: `ramen_global_action_consensus_status` gauge, 1 =
  reached, 0 = blocked.
- **Prometheus alert**: `GlobalActionConsensusBlocked`, fires when metric
  stays 0 for 10 minutes with severity warning.

## 6. Flows

The following flows use app-1 and app-2 sharing the same global replication
group. Each has its own DRPC on the hub and its own VRG on each managed
cluster, but both share a single global VGR per cluster.

### 6.1 Enable (Initial Deployment)

1. DRPCs for app-1 and app-2 are created on the hub. VRGs for each are
    created on primary (cluster-1) and secondary (cluster-2) via ManifestWork.
1. **Primary cluster (cluster-1)**: Each VRG reconciles, finds its PVCs,
    detects global PeerClass, and labels itself with `global-vgr =
    global-<grID>`. The first VRG to reconcile creates the global VGR in the
    operator namespace with `replicationState=Primary`. The other VRG
    attempts to create the same VGR, gets "already exists", and picks up the
    existing one.
1. **Hub**: Each DRPC reconciles, reads the `global-vgr` label from its
    primary VRG, and copies it onto itself. On next reconcile, each DRPC
    propagates the label to its secondary VRG via ManifestWork.
1. **Secondary cluster (cluster-2)**: Both VRGs receive the `global-vgr`
    label via ManifestWork. But no PVCs exist on cluster-2, so **no VGR is
    created**. Both VRGs are in standby with the label.

If a new application (app-3) is DR-enabled later using the same replication
group, it follows the same steps. The global VGR already exists, so app-3's
VRG picks up the existing one. Once labeled, app-3 joins the consensus
group and future failover or relocate requires all DRPCs (including app-3)
to agree.

**Result**:

| Cluster | Global VGR | VRGs with label | PVCs |
|---------|-----------|-----------------|------|
| cluster-1 (primary) | `global-<grID>`, replicationState=Primary | app-1, app-2 | Yes |
| cluster-2 (secondary) | None | app-1, app-2 (from DRPC) | No |

### 6.2 Failover

Scenario: cluster-1 is down, failover to cluster-2.

1. Admin sets `Action=Failover, FailoverCluster=cluster-2` on **both**
    DRPCs (app-1, app-2).
1. **DRPC consensus**: Suppose app-1's DRPC is updated first. It checks
    peers and finds app-2 hasn't been updated yet, so it enters
    `WaitOnGlobalAction`. The DRPC watcher instantly notifies peers as each is
    updated. When app-2's DRPC is updated, both agree on action + target.
    Consensus is reached and failover proceeds.
1. **cluster-2**: Both VRGs are updated to `replicationState=Primary`.
    VRG-level consensus confirms both VRGs want Primary.
1. **VGR created on cluster-2**: The first VRG to reconcile creates the
    global VGR in the operator namespace with `replicationState=Primary`.
    This is the first time a VGR exists on cluster-2.
1. The external storage provider transitions VGR `.status.state` to Primary.
    Both VRGs see the state match, mark their PVCs as DataReady and
    DataProtected.
1. **Cleanup on cluster-1** (if accessible): Both VRGs transition to
    Secondary. The existing global VGR is updated to
    `replicationState=Secondary`. The external storage provider transitions
    VGR `.status.state` accordingly. This ensures cluster-1 is properly
    configured as a secondary and ready to serve as a failover target in the
    future.

**Result**:

| Cluster | Global VGR | Role |
|---------|-----------|------|
| cluster-1 | `global-<grID>`, replicationState=Secondary | Old primary |
| cluster-2 | `global-<grID>`, replicationState=Primary | New primary (VGR just created) |

### 6.3 Relocate

Scenario: relocate from cluster-1 (current primary) to cluster-2.

1. Admin sets `Action=Relocate, PreferredCluster=cluster-2` on both DRPCs.
1. **DRPC consensus**: Same as failover. The DRPC updated first enters
    `WaitOnGlobalAction` until both agree on action + target. Watcher notifies
    peers instantly.
1. **Both clusters become Secondary**: Ramen first ensures all VRGs on both
    clusters are secondary. On cluster-1 (source), both VRGs transition to
    Secondary. GlobalState consensus is checked — the VGR is only updated to
    secondary once all VRGs on the cluster agree on the secondary state.
    Global VGR on cluster-1 is updated to `replicationState=Secondary`. The
    external storage provider transitions VGR `.status.state` accordingly.
    cluster-2 VRGs are already secondary.
1. **cluster-2 promoted to Primary**: Once both clusters are confirmed
    secondary, Ramen promotes cluster-2. Both VRGs on cluster-2 transition to
    Primary. VRG-level consensus confirms both agree. If no VGR exists on
    cluster-2 (first-time target), it is **created** with
    `replicationState=Primary`. If a VGR already exists from a prior failover,
    it is **updated** to `replicationState=Primary`.
1. The external storage provider transitions VGR `.status.state` to Primary
    on cluster-2. Both VRGs complete.

**Result**: cluster-2 is primary, cluster-1 is secondary. Both clusters now
have a global VGR. These VGRs persist through any future failover/relocate
operations.

### 6.4 Disable (Delete)

1. VRGs for both apps are deleted on both clusters.
1. Each VRG entering cleanup removes Ramen finalizers/annotations from its
    own PVCs.
1. Before deleting the global VGR, each VRG checks **deletion consensus**:
    all VRGs with the same `global-vgr` label must have a deletion
    timestamp.
1. If any peer VRG is still alive, the VGR is preserved and the deleting VRG
    requeues. For example, if app-1 is being deleted but app-2 is not, the
    global VGR stays alive for app-2.
1. Only when **both** VRGs are being deleted does the last VRG to finish
    cleanup delete the global VGR.
1. On the hub, DRPC finalization cleans up the `GlobalAction` Prometheus
    metric.
