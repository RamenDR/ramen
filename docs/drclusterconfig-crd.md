<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-1.0
-->

# DRClusterConfig CRD

## Overview

The **DRClusterConfig** custom resource provides cluster-specific
disaster recovery configuration on managed clusters. It is a
cluster-scoped resource that exists on each managed cluster (not on
the hub) and serves two primary purposes:

1. **Configuration Communication**: Conveys the cluster identity and
    desired replication schedules from the hub to the managed cluster
1. **Capability Discovery**: Scans the cluster for storage-related classes
    (StorageClass, VolumeSnapshotClass, VolumeReplicationClass, etc.) that
    have the required Ramen labels/annotations and reports them in its status.
    See the Status Fields section below for the specific label/annotation
    requirements for each class type.

**Note:** DRClusterConfig does not create these classes. Storage providers
or administrators must create and label/annotate the classes appropriately.
DRClusterConfig only discovers and reports them in its status.

**Lifecycle:** Automatically created and managed by Ramen on each
managed cluster. Users typically don't create this resource manually.

**Resource Name:** The DRClusterConfig resource name matches the name of
the managed cluster. For example, if the managed cluster is named "cluster1",
the DRClusterConfig resource will also be named "cluster1".

## API Group and Version

- **API Group:** `ramendr.openshift.io`
- **API Version:** `v1alpha1`
- **Kind:** `DRClusterConfig`
- **Scope:** Cluster (on managed clusters)

## Spec Fields

### Required Fields

#### `clusterID` (string)

The unique identifier for this cluster, derived from the kube-system
namespace UID.

**Source:** OCM ManagedCluster claim value for `id.k8s.io`

**Requirements:**

- Required field
- Immutable after creation
- Must be globally unique

**Example:**

```yaml
clusterID: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
```

**Purpose:** Used to correlate storage resources across clusters and
identify the cluster in S3 metadata.

### Optional Fields

#### `replicationSchedules` ([]string)

List of desired replication schedules that storage providers should support.

**Format:** `<num><m,h,d>` where:

- `m` = minutes
- `h` = hours
- `d` = days

**Example:**

```yaml
replicationSchedules:
  - "5m"
```

## Status Fields

The DRClusterConfig status is populated by the
Ramen DR cluster operator, which scans the cluster for classes
with the required labels/annotations and updates the status accordingly.

### `conditions` ([]metav1.Condition)

Standard Kubernetes conditions array.

**Condition type:**

- `Processed` - Indicates whether the DRClusterConfig configuration has been processed

**Condition reasons:**

- `"Initializing"` - Condition is being initialized
- `"Succeeded"` - Configuration processed successfully
- `"Failed"` - Configuration processing failed

### `storageClasses` ([]string)

List of StorageClass names that have the `ramendr.openshift.io/storageid` label.

**Example:**

```yaml
storageClasses:
  - block-storage
  - file-storage
```

**Purpose:** Reports available storage classes to the hub for peer class discovery.

### `volumeSnapshotClasses` ([]string)

List of VolumeSnapshotClass names with the `ramendr.openshift.io/storageid` label.

**Example:**

```yaml
volumeSnapshotClasses:
  - csi-fileplugin-snapclass
  - csi-blockplugin-snapclass
```

**Purpose:** Used for sync DR (Metro DR) peer class matching.

### `volumeGroupSnapshotClasses` ([]string)

List of VolumeGroupSnapshotClass names with the
`ramendr.openshift.io/storageid` label.

**Example:**

```yaml
volumeGroupSnapshotClasses:
  - csi-vg-snapclass
```

**Purpose:** Enables crash-consistent snapshots across multiple PVCs.

### `volumeReplicationClasses` ([]string)

List of VolumeReplicationClass names with the
`ramendr.openshift.io/replicationid` label.

**Example:**

```yaml
volumeReplicationClasses:
  - block-replication-1h
  - block-replication-5m
```

**Purpose:** Used for async DR (Regional DR) peer class matching.

### `volumeGroupReplicationClasses` ([]string)

List of VolumeGroupReplicationClass names with the
`ramendr.openshift.io/groupreplicationid` label.

**Example:**

```yaml
volumeGroupReplicationClasses:
  - block-group-replication
```

**Purpose:** Enables consistency group replication for multiple PVCs.

### `networkFenceClasses` ([]string)

List of NetworkFenceClass names with the
`ramendr.openshift.io/storageid` annotation.

**Example:**

```yaml
networkFenceClasses:
  - network-fence-class
```

**Purpose:** Used by Metro DR for network-based cluster fencing.

## Examples

### DRClusterConfig with All Class Types

DRClusterConfig on a managed cluster with full storage capabilities:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRClusterConfig
metadata:
  name: cluster1
spec:
  clusterID: "b2c3d4e5-f6a7-8901-bcde-f12345678901"
  replicationSchedules:
    - "5m"
status:
  conditions:
    - type: Processed
      status: "True"
      reason: "Succeeded"
      message: "Configuration processed and validated"
      observedGeneration: 1
      lastTransitionTime: "2024-01-15T10:00:00Z"
  storageClasses:
    - block-storage
    - file-storage
  volumeSnapshotClasses:
    - csi-blockplugin-snapclass
    - csi-fileplugin-snapclass
  volumeGroupSnapshotClasses:
    - csi-vg-snapclass
  volumeReplicationClasses:
    - block-replication-5m
    - block-replication-30m
    - block-replication-1h
  volumeGroupReplicationClasses:
    - block-group-replication
  networkFenceClasses:
    - network-fence-class
```

## Monitoring

### Check Configuration Status

```bash
kubectl get drclusterconfig cluster1 -o yaml
```

### Verify Processing

```bash
# Check Processed condition
kubectl get drclusterconfig cluster1 -o jsonpath='{.status.conditions[?(@.type=="Processed")].status}'
```

### Monitor Class Discovery

```bash
# Watch for new classes being discovered
kubectl get drclusterconfig cluster1 -o jsonpath='{.status}' | jq
```

## Troubleshooting

### No Classes Discovered

**Symptom:** Status shows empty arrays for all class types.

**Check:**

1. **Verify classes exist in the cluster:**

   ```bash
   # Check all class types
   kubectl get storageclass
   kubectl get volumesnapshotclass
   kubectl get volumegroupsnapshotclass
   kubectl get volumereplicationclass
   kubectl get volumegroupreplicationclass
   kubectl get networkfenceclass
   ```

1. **Verify classes have required labels/annotations:**

   ```bash
   # Check for required labels/annotations on all class types
   kubectl get storageclass -o yaml | grep "ramendr.openshift.io"
   kubectl get volumesnapshotclass -o yaml | grep "ramendr.openshift.io"
   kubectl get volumegroupsnapshotclass -o yaml | grep "ramendr.openshift.io"
   kubectl get volumereplicationclass -o yaml | grep "ramendr.openshift.io"
   kubectl get volumegroupreplicationclass -o yaml | grep "ramendr.openshift.io"
   kubectl get networkfenceclass -o yaml | grep "ramendr.openshift.io"
   ```

1. **Verify Ramen DR cluster operator is running:**

   ```bash
   kubectl get pods -n ramen-system
   ```

1. **Check Ramen DR cluster operator logs:**

   ```bash
   kubectl logs -n ramen-system <pod-name> | grep -i "drclusterconfig"
   ```

**Solution:** Ensure classes exist in the cluster and have the required
labels/annotations as specified in the Overview section. Classes without
the required labels/annotations will not be discovered by DRClusterConfig.

## Related Resources

- [DRCluster](drcluster-crd.md) - Hub-side cluster configuration
- [DRPolicy](drpolicy-crd.md) - Uses peer classes discovered via DRClusterConfig
- [VolumeReplicationGroup](vrg-crd.md) - Consumes VolumeReplicationClass resources
- [Configuration Guide](configure.md) - S3 and storage configuration
