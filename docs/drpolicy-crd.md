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
- The replication schedule for Async (Regional DR)
- Storage class selectors for volume replication
- Whether to use async (Regional DR) or sync (Metro DR)

**Lifecycle:** DRPolicy resources are created once during DR setup and are
typically long-lived.

## API Group and Version

- **API Group:** `ramendr.openshift.io`
- **API Version:** `v1alpha1`
- **Kind:** `DRPolicy`
- **Scope:** Cluster

## Spec Fields

### Required Fields

#### `drClusters` ([]string)

List of exactly two DRCluster resource names that participate in this DR policy.

**Requirements:**

- Must contain exactly 2 DRCluster resource names
- DRCluster resources must exist on the hub cluster (see [DRCluster](drcluster-crd.md))
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
Async (Regional DR).

**Format:** `<number><m|h|d>` where:

- `m` = minutes
- `h` = hours
- `d` = days

**Behavior:**

- Empty string (`""`) = Sync (Metro DR)
- Non-empty = Async (Regional DR)
- Immutable after creation

**Examples:**

```yaml
schedulingInterval: "5m"   # Every 5 minutes
schedulingInterval: "1h"   # Every hour
schedulingInterval: "12h"  # Every 12 hours
schedulingInterval: ""     # Synchronous (no scheduling)
```

**Typical values:**

- Production Async (Regional DR): `"1h"` or `"30m"`
- Testing: `"5m"` or `"10m"`
- Sync (Metro DR): `""` (empty)

#### `replicationClassSelector` (metav1.LabelSelector)

Label selector to identify VolumeReplicationClass resources for
Async (Regional DR).

**When to use:** Set this for Async (Regional DR) when using
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

Label selector to identify VolumeSnapshotClass resources for
Async (Regional DR).

**When to use:** Set this for Async (Regional DR) when using VolSync
with schedulingInterval.

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
snapshot operations with VolSync in Async (Regional DR).

**When to use:** For Async (Regional DR) workloads with multiple PVCs that need
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

Status of async (Regional DR) details between clusters.

**Fields:**

- `peerClasses` ([]PeerClass) - List of common StorageClasses with async relationships

### `sync` (Sync)

Status of sync (Metro DR) details between clusters.

**Fields:**

- `peerClasses` ([]PeerClass) - List of common StorageClasses with sync relationships

### PeerClass Structure

Discovered peer relationship information between peer clusters:

- `storageClassName` - Common StorageClass name across peers
- `replicationID` - Common replication ID from VolumeReplicationClass labels
- `groupReplicationID` - Group replication ID for volume groups
- `storageID` - Storage instance IDs (singleton if shared, distinct if separate)
- `clusterIDs` - The two cluster IDs (kube-system namespace UIDs)
- `grouping` - Whether PVCs can be grouped for replication
- `offloaded` - Whether replication is managed externally (not by VRG)

## Examples

### Example 1: Async (Regional DR)

This policy configures Async (Regional DR) between geographically distributed
clusters:

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

  # Select VolumeReplicationClass
  replicationClassSelector:
    matchLabels:
      ramendr.openshift.io/replication-class: rbd-replication
```

### Example 2: Sync (Metro DR)

This policy configures Sync (Metro DR) between clusters in the same metro area:

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
```

**Note:** Sync (Metro DR) uses synchronous replication at the storage level.
VolSync is used for Async (Regional DR) and should not be used with
Sync (Metro DR).

## Troubleshooting

### DRPolicy Not Validated

Check the DRPolicy status:

```bash
kubectl get drpolicy <name> -o yaml
```

**Common issues:**

1. Clusters not registered in OCM

   Verify managed clusters are registered:

   ```bash
   kubectl get managedclusters
   ```

1. DRCluster resources not validated

   Verify DRCluster resources referenced in the DRPolicy are validated:

   ```bash
   kubectl get drcluster <cluster-name> -o yaml
   ```

   Check for `Validated` condition in status:

   ```yaml
   status:
     conditions:
       - type: Validated
         status: "True"
   ```

### No PeerClasses in Status

**Cause:** Classes available on managed clusters may not have required labels
or annotations, or replication isn't configured.

**For details on required labels and annotations, see [DRClusterConfig](drclusterconfig-crd.md).**

## Related Resources

- [DRPlacementControl](drpc-crd.md) - References DRPolicy for application DR
- [DRCluster](drcluster-crd.md) - Represents managed clusters in the policy
- [VolumeReplicationGroup](vrg-crd.md) - Created by DRPC, uses policy configuration
- [Configuration Guide](configure.md) - How to configure Ramen with DRPolicy
