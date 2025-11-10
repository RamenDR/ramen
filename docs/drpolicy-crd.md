<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-1.0
-->

# DRPolicy CRD

## Overview

The **DRPolicy** custom resource defines the disaster recovery topology and
replication configuration between peer clusters. It is a cluster-scoped
resource created by administrators on the OCM hub cluster that establishes
the DR relationship between two managed clusters.

A DRPolicy specifies:

- Which two clusters participate in DR
- The replication schedule (for async DR)
- Storage class selectors for volume replication
- Whether to use async (VolumeReplication) or sync (VolSync) replication

**Lifecycle:** DRPolicy resources are created once during DR setup and are
typically long-lived. Multiple DRPlacementControl resources can reference
the same DRPolicy.

## API Group and Version

- **API Group:** `ramendr.openshift.io`
- **API Version:** `v1alpha1`
- **Kind:** `DRPolicy`
- **Scope:** Cluster

## Spec Fields

### Required Fields

#### `drClusters` ([]string)

List of exactly two managed cluster names that participate in this DR policy.

**Requirements:**

- Must contain exactly 2 cluster names
- Cluster names must match OCM ManagedCluster resource names
- Immutable after creation

**Example:**

```yaml
drClusters:
  - east-cluster
  - west-cluster
```

### Optional Fields

#### `schedulingInterval` (string)

Defines how frequently to replicate volume data between clusters for
async replication.

**Format:** `<number><m|h|d>` where:

- `m` = minutes
- `h` = hours
- `d` = days

**Behavior:**

- Empty string (`""`) = synchronous replication (Metro DR / VolSync)
- Non-empty = asynchronous replication (Regional DR / VolumeReplication)
- Immutable after creation

**Examples:**

```yaml
schedulingInterval: "5m"   # Every 5 minutes
schedulingInterval: "1h"   # Every hour
schedulingInterval: "12h"  # Every 12 hours
schedulingInterval: ""     # Synchronous (no scheduling)
```

**Typical values:**

- Production async: `"1h"` or `"30m"`
- Testing: `"5m"` or `"10m"`
- Sync DR: `""` (empty)

#### `replicationClassSelector` (metav1.LabelSelector)

Label selector to identify VolumeReplicationClass resources for
async replication.

**When to use:** Set this for async (Regional DR) when using
VolumeReplication with schedulingInterval.

**How it works:**

- VRG uses this selector to find the appropriate VolumeReplicationClass
    for each PVC
- VolumeReplicationClass must exist on managed clusters with matching labels

**Example:**

```yaml
replicationClassSelector:
  matchLabels:
    ramendr.openshift.io/replication-class: rbd-replication
```

**Immutable:** Cannot be changed after creation (presence/absence only).

#### `volumeSnapshotClassSelector` (metav1.LabelSelector)

Label selector to identify VolumeSnapshotClass resources for sync
replication.

**When to use:** Set this for sync (Metro DR) when using VolSync
without schedulingInterval.

**How it works:**

- VRG uses this selector to find the appropriate VolumeSnapshotClass
    for snapshots
- VolumeSnapshotClass must exist on managed clusters with matching labels

**Example:**

```yaml
volumeSnapshotClassSelector:
  matchLabels:
    ramendr.openshift.io/snapshot-class: csi-snapclass
```

**Immutable:** Cannot be changed after creation (presence/absence only).

#### `volumeGroupSnapshotClassSelector` (metav1.LabelSelector)

Label selector to identify VolumeGroupSnapshotClass resources for grouped
snapshot operations with VolSync.

**When to use:** For workloads with multiple PVCs that need
consistent point-in-time snapshots.

**Example:**

```yaml
volumeGroupSnapshotClassSelector:
  matchLabels:
    ramendr.openshift.io/vg-snapshot-class: csi-vg-snapclass
```

## Status Fields

The DRPolicy status is automatically populated by the Ramen hub operator.

### `conditions` ([]metav1.Condition)

Standard Kubernetes conditions indicating the policy validation state.

**Common condition types:**

- `Validated` - DRPolicy has been validated successfully

### `async` (Async)

Status of async replication details between clusters (for Regional DR).

**Fields:**

- `peerClasses` ([]PeerClass) - List of common StorageClasses with async relationships

### `sync` (Sync)

Status of sync replication details between clusters (for Metro DR).

**Fields:**

- `peerClasses` ([]PeerClass) - List of common StorageClasses with sync relationships

### PeerClass Structure

Discovered storage class relationships between peer clusters:

- `storageClassName` - Common StorageClass name across peers
- `replicationID` - Common replication ID from VolumeReplicationClass labels
- `groupReplicationID` - Group replication ID for volume groups
- `storageID` - Storage instance IDs (singleton if shared, distinct if separate)
- `clusterIDs` - The two cluster IDs (kube-system namespace UIDs)
- `grouping` - Whether PVCs can be grouped for replication
- `offloaded` - Whether replication is managed externally (not by VRG)

## Examples

### Example 1: Async Regional DR Policy

For asynchronous replication between geographically distributed
clusters using Ceph RBD mirroring:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPolicy
metadata:
  name: regional-dr-policy
spec:
  # Replicate every hour
  schedulingInterval: "1h"

  # Two clusters in different regions
  drClusters:
    - us-east-cluster
    - us-west-cluster

  # Select VolumeReplicationClass for Ceph RBD
  replicationClassSelector:
    matchLabels:
      ramendr.openshift.io/replication-class: rbd-replication
```

### Example 2: Sync Metro DR Policy

For synchronous replication between clusters in the same metro area using VolSync:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPolicy
metadata:
  name: metro-dr-policy
spec:
  # Empty scheduling interval = synchronous
  schedulingInterval: ""

  # Two clusters in the same metro
  drClusters:
    - metro-cluster-1
    - metro-cluster-2

  # Select VolumeSnapshotClass for VolSync
  volumeSnapshotClassSelector:
    matchLabels:
      ramendr.openshift.io/snapshot-class: csi-snapclass
```

### Example 3: Multi-Policy Setup

You can have multiple DRPolicies for different cluster pairs:

```yaml
# Policy for US East <-> US West
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPolicy
metadata:
  name: us-regional-dr
spec:
  schedulingInterval: "1h"
  drClusters:
    - us-east
    - us-west
  replicationClassSelector:
    matchLabels:
      class: rbd-replication
---
# Policy for EU clusters
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPolicy
metadata:
  name: eu-regional-dr
spec:
  schedulingInterval: "1h"
  drClusters:
    - eu-north
    - eu-south
  replicationClassSelector:
    matchLabels:
      class: rbd-replication
```

## Usage

### Creating a DRPolicy

**Prerequisites:**

1. Two managed clusters registered with OCM hub
1. Storage with replication support on both clusters
1. VolumeReplicationClass (async) or VolumeSnapshotClass (sync) configured

**Steps:**

1. Verify managed clusters are ready:

   ```bash
   kubectl get managedclusters
   ```

1. Create the DRPolicy:

   ```bash
   kubectl apply -f drpolicy.yaml
   ```

1. Verify the policy is validated:

   ```bash
   kubectl get drpolicy regional-dr-policy -o yaml
   ```

   Check for `Validated` condition in status:

   ```yaml
   status:
     conditions:
       - type: Validated
         status: "True"
   ```

1. Check discovered peer classes:

   ```bash
   kubectl get drpolicy regional-dr-policy -o jsonpath='{.status.async.peerClasses}' | jq
   ```

### Referencing a DRPolicy

DRPlacementControl resources reference DRPolicy by name:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: my-app-drpc
  namespace: my-app
spec:
  drPolicyRef:
    name: regional-dr-policy # References the DRPolicy
  # ... other DRPC fields
```

## Validation Rules

The DRPolicy has built-in validation:

1. **drClusters must contain exactly 2 clusters**

    - Error: "drClusters requires a list of 2 clusters"

1. **Fields are immutable after creation:**

    - `drClusters` - Cannot change cluster list
    - `schedulingInterval` - Cannot switch between sync/async
    - `replicationClassSelector` - Presence cannot change
    - `volumeSnapshotClassSelector` - Presence cannot change

1. **schedulingInterval format validation:**
    - Must match pattern: `^\d+[mhd]$` or empty string
    - Examples: `5m`, `1h`, `2d`, or `""`

## Troubleshooting

### DRPolicy Not Validated

**Check:**

```bash
kubectl get drpolicy <name> -o yaml
```

**Common issues:**

1. Clusters not registered in OCM

   ```bash
   kubectl get managedclusters
   ```

1. VolumeReplicationClass or VolumeSnapshotClass not found

   ```bash
   # On each managed cluster
   kubectl get volumereplicationclass
   kubectl get volumesnapshotclass
   ```

1. Storage classes not properly labeled

   ```bash
   kubectl get sc -o yaml | grep "ramendr.openshift.io"
   ```

### No PeerClasses in Status

**Cause:** Storage classes may not have required labels or replication isn't configured.

**Check storage class labels on managed clusters:**

```bash
kubectl get sc <storage-class-name> -o yaml
```

**Required labels:**

- `ramendr.openshift.io/storageid` - Storage backend identifier
- `ramendr.openshift.io/replicationid` - Replication backend identifier (on VolumeReplicationClass)

### Cannot Delete DRPolicy

**Cause:** DRPlacementControl resources are still referencing it.

**Check references:**

```bash
kubectl get drpc -A -o yaml | grep drPolicyRef
```

**Solution:** Delete all referencing DRPCs first, then delete the DRPolicy.

## Best Practices

1. **Use descriptive names** - Include location or purpose in the
    name (e.g., `us-regional-dr`, `metro-dr-policy`)

1. **Set appropriate scheduling intervals:**

    - Production: 30m to 1h for most workloads
    - High-frequency: 5m to 15m for critical workloads
    - Low-frequency: 6h to 24h for large datasets

1. **Label storage classes consistently** across clusters for peer class discovery

1. **Create separate policies** for different DR requirements
    (e.g., one for US clusters, one for EU clusters)

1. **Test policies** with non-production workloads before using in production

1. **Monitor policy status** regularly to ensure validation remains successful

## Related Resources

- [DRPlacementControl](drpc-crd.md) - References DRPolicy for application DR
- [DRCluster](drcluster-crd.md) - Represents managed clusters in the policy
- [VolumeReplicationGroup](vrg-crd.md) - Created by DRPC, uses policy configuration
- [Configuration Guide](configure.md) - How to configure Ramen with DRPolicy
