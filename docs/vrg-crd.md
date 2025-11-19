<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-1.0
-->

# VolumeReplicationGroup CRD

## Overview

The **VolumeReplicationGroup** (VRG) custom resource manages volume
replication and kubernetes object protection for an application on a
managed cluster. VRGs are **not directly created by users** - they are
automatically created and managed by the DRPlacementControl (DRPC) on the
hub cluster via ManifestWork.

A VRG controls:

- Volume replication state (Primary/Secondary) for all PVCs matching the selector
- VolumeReplication (async) or VolSync (sync) resource creation and management
- PV metadata storage in S3 for cross-cluster recovery
- Kubernetes object capture and recovery (via Velero or Recipe)

**Lifecycle:** Created by DRPC when enabling DR protection. Exists on the
active cluster as Primary and on the peer cluster as Secondary. Deleted
when DRPC is removed.

## API Group and Version

- **API Group:** `ramendr.openshift.io`
- **API Version:** `v1alpha1`
- **Kind:** `VolumeReplicationGroup`
- **Short Name:** `vrg`
- **Scope:** Namespaced (on managed clusters)

## Spec Fields

### Required Fields

#### `pvcSelector` (metav1.LabelSelector)

Label selector to identify PVCs that should be replicated as part of this group.

**Example:**

```yaml
pvcSelector:
  matchLabels:
    app: myapp
```

#### `replicationState` (ReplicationState)

Desired replication state for all volumes in this group.

**Valid values:**

- `primary` - Volumes are primary (writable), source of replication
- `secondary` - Volumes are secondary (read-only), target of replication

**Example:**

```yaml
replicationState: primary
```

**Important:** This is managed by DRPC. When DRPC fails over, it changes
this from primary→secondary on the source and secondary→primary on the
target.

#### `s3Profiles` ([]string)

List of S3 profile names (from RamenConfig) used to store PV metadata.

**Example:**

```yaml
s3Profiles:
  - s3-profile-east
```

**Purpose:** PV specs are stored in S3 so the peer cluster
    can recreate them during failover.

### Optional Fields

#### `async` (VRGAsyncSpec)

Configuration for asynchronous replication using VolumeReplication.

**Fields:**

- `replicationClassSelector` (metav1.LabelSelector) - Selects
    VolumeReplicationClass
- `volumeSnapshotClassSelector` (metav1.LabelSelector) -
    Selects VolumeSnapshotClass (for VolSync async)
- `volumeGroupSnapshotClassSelector` (metav1.LabelSelector) - For volume
    group snapshots
- `schedulingInterval` (string) - Replication frequency (e.g., "1h", "30m")
- `peerClasses` ([]PeerClass) - Storage class peer relationships

**Example:**

```yaml
async:
  schedulingInterval: "1h"
  replicationClassSelector:
    matchLabels:
      ramendr.openshift.io/replication-class: rbd-replication
```

#### `sync` (VRGSyncSpec)

Configuration for synchronous replication.

**Fields:**

- `peerClasses` ([]PeerClass) - Storage class peer relationships

**Example:**

```yaml
sync:
  peerClasses:
    - storageClassName: csi-cephfs
      replicationID: cephfs-replication
```

#### `volSync` (VolSyncSpec)

Configuration for VolSync-based replication.

**Fields:**

- `disabled` (bool) - Set to true to bypass VolSync
- `rdSpec` ([]VolSyncReplicationDestinationSpec) -
    ReplicationDestination specs for Secondary VRG
- `moverConfig` ([]MoverConfig) - Advanced VolSync mover configuration

**Example:**

```yaml
volSync:
  disabled: false
```

#### `action` (VRGAction)

The DR action being performed: `Failover` or `Relocate`.

**Values:**

- `Failover` - Unplanned recovery
- `Relocate` - Planned migration

**Managed by:** DRPC sets this field.

#### `kubeObjectProtection` (KubeObjectProtectionSpec)

Configuration for protecting Kubernetes objects (not just PVCs).

**Fields:**

- `captureInterval` (metav1.Duration) - How often to capture objects (default: 5m)
- `recipeRef` (RecipeRef) - Reference to Recipe for custom workflows
- `recipeParameters` (map[string][]string) - Parameters for Recipe
- `kubeObjectSelector` (metav1.LabelSelector) - Selector for objects to protect

**Example:**

```yaml
kubeObjectProtection:
  captureInterval: 5m
  recipeRef:
    name: mysql-recipe
    namespace: mysql-app
```

#### `protectedNamespaces` ([]string)

List of namespaces to protect beyond the VRG namespace.

**Requirements:**

- VRG must be in RamenOpsNamespace
- Resources treated as unmanaged
- Typically used with Recipes

**Example:**

```yaml
protectedNamespaces:
  - app-frontend
  - app-backend
```

#### `prepareForFinalSync` (bool)

Indicates VRG should prepare for the final sync during relocate (VolSync only).

**Managed by:** DRPC sets this during relocate operations.

#### `runFinalSync` (bool)

Indicates VRG should perform the final sync during relocate (VolSync only).

**Managed by:** DRPC sets this during relocate operations.

## Status Fields

### `state` (State)

Current replication state of the VRG.

**Values:**

- `Primary` - VRG is successfully primary
- `Secondary` - VRG is successfully secondary
- `Unknown` - State cannot be determined

### `protectedPVCs` ([]ProtectedPVC)

List of PVCs that are protected by this VRG with their status.

**ProtectedPVC fields:**

- `name` - PVC name
- `namespace` - PVC namespace
- `protectedByVolSync` - Whether using VolSync for this PVC
- `storageClassName` - StorageClass used
- `accessModes` - PVC access modes
- `resources` - Resource requirements
- `conditions` - PVC-specific conditions
- `lastSyncTime` - Most recent sync time
- `lastSyncDuration` - Duration of last sync
- `lastSyncBytes` - Bytes transferred in last sync

**Example output:**

```yaml
protectedPVCs:
  - name: mysql-pvc
    namespace: mysql-app
    protectedByVolSync: false
    storageClassName: ceph-rbd
    lastSyncTime: "2024-01-15T10:30:00Z"
    lastSyncDuration: 2m15s
    lastSyncBytes: 1073741824
    conditions:
      - type: Replicating
        status: "True"
```

### `pvcgroups` ([]Groups)

List of PVC groups for consistency group replication.

### `conditions` ([]metav1.Condition)

Standard Kubernetes conditions for the VRG.

**Common condition types:**

- `ClusterDataProtected` - PV data stored in S3
- `DataReady` - Volumes ready for use
- `DataProtected` - Volume replication healthy

### `observedGeneration` (int64)

The generation of the VRG spec that was last processed.

### `lastUpdateTime` (metav1.Time)

When the status was last updated.

### `lastGroupSyncTime` (metav1.Time)

Time of the most recent successful synchronization of all PVCs.

**Use:** Monitor to ensure replication is occurring.

### `lastGroupSyncDuration` (metav1.Duration)

Duration of the most recent group sync.

### `lastGroupSyncBytes` (int64)

Total bytes transferred in the most recent group sync.

### `kubeObjectProtection` (KubeObjectProtectionStatus)

Status of Kubernetes object protection.

**Fields:**

- `captureToRecoverFrom` - Identifier of the capture to use for recovery

### `prepareForFinalSyncComplete` (bool)

Whether prepare for final sync has completed (VolSync relocate).

### `finalSyncComplete` (bool)

Whether the final sync has completed (VolSync relocate).

## Examples

### Example 1: Primary VRG (Managed Cluster)

This is what DRPC creates on the active cluster:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: VolumeReplicationGroup
metadata:
  name: webapp-drpc
  namespace: webapp
spec:
  replicationState: primary
  pvcSelector:
    matchLabels:
      app: webapp
  s3Profiles:
    - s3-profile-east
  async:
    schedulingInterval: "1h"
    replicationClassSelector:
      matchLabels:
        ramendr.openshift.io/replication-class: rbd-replication
  action: Relocate
  kubeObjectProtection:
    captureInterval: 5m
```

### Example 2: Secondary VRG (Peer Cluster)

This is what DRPC creates on the standby cluster:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: VolumeReplicationGroup
metadata:
  name: webapp-drpc
  namespace: webapp
spec:
  replicationState: secondary
  pvcSelector:
    matchLabels:
      app: webapp
  s3Profiles:
    - s3-profile-west
  async:
    schedulingInterval: "1h"
    replicationClassSelector:
      matchLabels:
        ramendr.openshift.io/replication-class: rbd-replication
  volSync:
    disabled: false
```

### Example 3: Recipe-Based VRG

VRG with Recipe for custom capture/recovery workflows:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: VolumeReplicationGroup
metadata:
  name: mysql-drpc
  namespace: mysql-app
spec:
  replicationState: primary
  pvcSelector:
    matchLabels:
      app: mysql
  s3Profiles:
    - s3-profile-east
  async:
    schedulingInterval: "30m"
    replicationClassSelector:
      matchLabels:
        ramendr.openshift.io/replication-class: rbd-replication
  kubeObjectProtection:
    captureInterval: 5m
    recipeRef:
      name: mysql-recipe
      namespace: mysql-app
```

## Understanding VRG Behavior

### Primary VRG

When `replicationState: primary`:

1. Creates VolumeReplication CRs for each PVC with `replicationState: primary`
1. Stores PV metadata in S3
1. Captures Kubernetes objects (if kubeObjectProtection is configured)
1. Application can read/write to volumes

### Secondary VRG

When `replicationState: secondary`:

1. Creates VolumeReplication CRs for each PVC with `replicationState: secondary`
1. Volumes are read-only (no application access)
1. Receives replicated data from primary
1. No Kubernetes object capture (secondary is standby)

### State Transitions

**Initial Deployment:**

- DRPC creates VRG on preferred cluster with `replicationState: primary`
- DRPC creates VRG on peer cluster with `replicationState: secondary`

**Failover:**

- DRPC changes peer VRG from `secondary` → `primary`
- Application deploys on peer cluster
- Source VRG remains or is deleted (if source is down)

**Relocate:**

- DRPC performs final sync on source
- DRPC changes source VRG from `primary` → `secondary`
- DRPC changes target VRG from `secondary` → `primary`
- Application redeploys on target

## Monitoring VRG

### Check VRG State

```bash
# On managed cluster
kubectl get vrg -n myapp
```

Output shows: name, desiredState, currentState.

### Check Detailed Status

```bash
kubectl get vrg myapp-drpc -n myapp -o yaml
```

### Check Protected PVCs

```bash
kubectl get vrg myapp-drpc -n myapp -o jsonpath='{.status.protectedPVCs}' | jq
```

### Check Replication Status

```bash
# Last sync time
kubectl get vrg myapp-drpc -n myapp -o jsonpath='{.status.lastGroupSyncTime}'

# Sync bytes transferred
kubectl get vrg myapp-drpc -n myapp -o jsonpath='{.status.lastGroupSyncBytes}'
```

### Check VolumeReplication Resources

```bash
# Created by VRG
kubectl get volumereplication -n myapp
kubectl describe volumereplication -n myapp
```

## Troubleshooting

### VRG Not Reaching Desired State

**Check status:**

```bash
kubectl get vrg myapp-drpc -n myapp -o yaml
```

**Common issues:**

1. **VolumeReplication CRs not created**

   ```bash
   kubectl get volumereplication -n myapp
   ```

    - Check if VolumeReplicationClass exists
    - Verify storage driver supports replication

1. **PVCs not found**

   ```bash
   kubectl get pvc -n myapp -l app=myapp
   ```

    - Verify pvcSelector matches actual PVC labels

1. **S3 access failing**
    - Check VRG operator logs:

        ```bash
        kubectl logs -n ramen-system deployment/ramen-dr-cluster-operator -c manager
        ```

    - Verify S3 profile configuration in RamenConfig

### Replication Not Working

**Check VolumeReplication status:**

```bash
kubectl get volumereplication -n myapp -o yaml
```

Look for conditions indicating replication state.

**Check underlying storage:**

```bash
# For Ceph RBD mirroring
kubectl exec -n rook-ceph deploy/rook-ceph-tools -- rbd mirror pool status replicapool
```

### Data Not Syncing

**Check lastGroupSyncTime:**

```bash
kubectl get vrg myapp-drpc -n myapp -o jsonpath='{.status.lastGroupSyncTime}'
```

**If sync time is null or stale:**

1. Check VRG is in primary state
1. Check VolumeReplication conditions
1. Verify storage replication is configured and healthy
1. Check for errors in VRG status conditions

### VRG Stuck in Deletion

**Check finalizers:**

```bash
kubectl get vrg myapp-drpc -n myapp -o jsonpath='{.metadata.finalizers}'
```

**Common causes:**

- VolumeReplication CRs not cleaned up
- S3 cleanup pending
- ManifestWork deletion pending

**View VRG operator logs:**

```bash
kubectl logs -n ramen-system deployment/ramen-dr-cluster-operator -c manager | grep myapp-drpc
```

## Best Practices

1. **Don't create VRGs manually** - Let DRPC manage them via ManifestWork

1. **Monitor VRG status regularly** - Check `lastGroupSyncTime`
    to ensure replication is working

1. **Check VRG state after DR operations** - Verify state transitions complete:

    - After failover: New primary should be `Primary`, old
        primary should be deleted or `Secondary`
    - After relocate: Target should be `Primary`, source
        should be `Secondary`

1. **Review protectedPVCs list** - Ensure all expected PVCs are included

1. **Monitor replication lag** - Use `lastGroupSyncTime` and
    `lastGroupSyncBytes` metrics

1. **Check conditions** - VRG conditions indicate issues with replication or S3 access

1. **Understand primary/secondary roles** - Only primary VRGs allow application writes

## Advanced Topics

### Volume Group Replication

For applications with multiple PVCs needing crash-consistent snapshots:

**Requirements:**

- Storage supports VolumeGroupReplicationClass or VolumeGroupSnapshotClass
- PVCs use compatible StorageClasses
- Properly configured in DRPolicy

**Status:**
Check `pvcgroups` in VRG status for grouped PVCs.

### S3 Metadata Storage

VRG stores these in S3:

- PV specs (for recreating PVs on peer cluster)
- VRG state metadata
- Protected PVC list

**S3 bucket structure:**

```
<bucket-name>/
  <cluster-id>/
    <namespace>/
      <vrg-name>/
        pv-<pv-name>.json
        vrg-metadata.json
```

### VolSync Integration

When using VolSync for sync DR:

- VRG creates ReplicationSource (primary) or ReplicationDestination (secondary)
- Final sync support for relocate operations
- `prepareForFinalSync` and `runFinalSync` coordinate the process

## Related Resources

- [DRPlacementControl](drpc-crd.md) - Creates and manages VRGs
- [DRPolicy](drpolicy-crd.md) - Defines replication configuration used by VRG
- [Usage Guide](usage.md) - How VRG fits into workload protection
- [Recipe Documentation](recipe.md) - For custom VRG workflows
