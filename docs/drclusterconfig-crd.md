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
1. **Capability Discovery**: Reports available storage classes,
    snapshot classes, and replication classes back to the hub for peer
    class matching

Storage providers watch DRClusterConfig to discover replication
requirements and create appropriate VolumeReplicationClass or
VolumeSnapshotClass resources. The hub cluster uses the discovered
capabilities to populate DRPolicy status with peer class information.

**Lifecycle:** Automatically created and managed by Ramen on each
managed cluster. Users typically don't create this resource manually.

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
  - "30m"
  - "1h"
```

**How it works:**

1. Hub cluster determines required schedules from DRPolicies
1. Sets these schedules in DRClusterConfig spec
1. Storage provider controllers watch DRClusterConfig
1. Providers create VolumeReplicationClass resources with matching schedules

## Status Fields

The DRClusterConfig status is populated by the
Ramen DR cluster operator and storage provider controllers.

### `conditions` ([]metav1.Condition)

Standard Kubernetes conditions.

**Condition types:**

- `Processed` - Configuration has been processed successfully
- `Reachable` - S3 storage is reachable from this cluster

### `storageClasses` ([]string)

List of StorageClass names that have the `ramendr.openshift.io/storageid` label.

**Example:**

```yaml
storageClasses:
  - ceph-rbd
  - ceph-cephfs
```

**Purpose:** Reports available storage classes to the hub for peer class discovery.

### `volumeSnapshotClasses` ([]string)

List of VolumeSnapshotClass names with the `ramendr.openshift.io/storageid` label.

**Example:**

```yaml
volumeSnapshotClasses:
  - csi-cephfsplugin-snapclass
  - csi-rbdplugin-snapclass
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
  - rbd-replication-1h
  - rbd-replication-5m
```

**Purpose:** Used for async DR (Regional DR) peer class matching.

### `volumeGroupReplicationClasses` ([]string)

List of VolumeGroupReplicationClass names with the
`ramendr.openshift.io/replicationid` label.

**Example:**

```yaml
volumeGroupReplicationClasses:
  - ceph-rbd-group-replication
```

**Purpose:** Enables consistency group replication for multiple PVCs.

### `networkFenceClasses` ([]string)

List of NetworkFence class names available for cluster fencing operations.

**Example:**

```yaml
networkFenceClasses:
  - fence-agents-network
```

**Purpose:** Used by Metro DR for network-based cluster fencing.

## Examples

### Example 1: Basic DRClusterConfig

Typical DRClusterConfig on a managed cluster:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRClusterConfig
metadata:
  name: drclusterconfig
spec:
  clusterID: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
  replicationSchedules:
    - "1h"
    - "5m"
status:
  conditions:
    - type: Processed
      status: "True"
    - type: Reachable
      status: "True"
  storageClasses:
    - ceph-rbd
  volumeSnapshotClasses:
    - csi-rbdplugin-snapclass
  volumeReplicationClasses:
    - rbd-replication-1h
    - rbd-replication-5m
```

### Example 2: Complete Configuration with All Class Types

DRClusterConfig with full storage capabilities:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRClusterConfig
metadata:
  name: drclusterconfig
spec:
  clusterID: "b2c3d4e5-f6a7-8901-bcde-f12345678901"
  replicationSchedules:
    - "5m"
    - "30m"
    - "1h"
status:
  conditions:
    - type: Processed
      status: "True"
      lastTransitionTime: "2024-01-15T10:00:00Z"
    - type: Reachable
      status: "True"
      lastTransitionTime: "2024-01-15T10:00:00Z"
  storageClasses:
    - ceph-rbd
    - ceph-cephfs
  volumeSnapshotClasses:
    - csi-rbdplugin-snapclass
    - csi-cephfsplugin-snapclass
  volumeGroupSnapshotClasses:
    - csi-vg-snapclass
  volumeReplicationClasses:
    - rbd-replication-5m
    - rbd-replication-30m
    - rbd-replication-1h
  volumeGroupReplicationClasses:
    - ceph-rbd-group-replication
  networkFenceClasses:
    - fence-agents-network
```

## How It Works

### Configuration Flow

1. **Hub → Managed Cluster:**

    - User creates DRPolicy on hub with `schedulingInterval: "1h"`
    - Hub operator determines required schedules
    - Hub creates/updates DRClusterConfig on managed cluster with
        `replicationSchedules: ["1h"]`

1. **Storage Provider Response:**

    - Ceph CSI operator watches DRClusterConfig
    - Sees `replicationSchedules: ["1h"]`
    - Creates VolumeReplicationClass with 1h schedule
    - Labels it with `ramendr.openshift.io/replicationid`

1. **Managed Cluster → Hub:**

    - Ramen DR cluster operator scans for labeled classes
    - Updates DRClusterConfig status with discovered classes
    - Hub reads status via ManagedClusterView

1. **Hub Processing:**
    - Hub compares status across all clusters in DRPolicy
    - Identifies common storage classes
    - Populates DRPolicy status with peer classes

### Storage Provider Integration

Storage providers should:

1. **Watch DRClusterConfig** for spec changes
1. **Create VolumeReplicationClass** resources matching requested schedules
1. **Label resources** appropriately:
    - `ramendr.openshift.io/storageid: <storage-id>` on StorageClass
    - `ramendr.openshift.io/replicationid: <replication-id>` on
        VolumeReplicationClass

**Example VolumeReplicationClass created by storage provider:**

```yaml
apiVersion: replication.storage.openshift.io/v1alpha1
kind: VolumeReplicationClass
metadata:
  name: rbd-replication-1h
  labels:
    ramendr.openshift.io/replicationid: ceph-replication
spec:
  provisioner: rbd.csi.ceph.com
  parameters:
    replication.storage.openshift.io/replication-secret-name: rook-csi-rbd-provisioner
    replication.storage.openshift.io/replication-secret-namespace: rook-ceph
    schedulingInterval: "1h"
```

## Usage

### Viewing DRClusterConfig

**On managed cluster:**

```bash
kubectl get drclusterconfig
kubectl describe drclusterconfig drclusterconfig
```

**From hub cluster (via ManagedClusterView):**

```bash
# Hub creates ManagedClusterView to read DRClusterConfig
kubectl get managedclusterview -n <cluster-namespace> | grep drclusterconfig
```

### Checking Discovered Classes

```bash
# View all discovered classes
kubectl get drclusterconfig drclusterconfig -o yaml

# Check specific class types
kubectl get drclusterconfig drclusterconfig -o jsonpath='{.status.volumeReplicationClasses}'
kubectl get drclusterconfig drclusterconfig -o jsonpath='{.status.storageClasses}'
```

### Verifying S3 Connectivity

```bash
kubectl get drclusterconfig drclusterconfig -o jsonpath='{.status.conditions}' | jq '.[] | select(.type=="Reachable")'
```

Expected output:

```json
{
  "type": "Reachable",
  "status": "True",
  "lastTransitionTime": "2024-01-15T10:00:00Z"
}
```

## Monitoring

### Check Configuration Status

```bash
kubectl get drclusterconfig drclusterconfig -o yaml
```

### Verify Processing

```bash
# Check Processed condition
kubectl get drclusterconfig drclusterconfig -o jsonpath='{.status.conditions[?(@.type=="Processed")].status}'
```

### Monitor Class Discovery

```bash
# Watch for new classes being discovered
kubectl get drclusterconfig drclusterconfig -o jsonpath='{.status}' | jq
```

## Troubleshooting

### No Classes Discovered

**Symptom:** Status shows empty arrays for all class types.

**Check:**

1. **Storage classes have correct labels:**

   ```bash
   kubectl get sc -o yaml | grep "ramendr.openshift.io/storageid"
   ```

1. **VolumeReplicationClass exists:**

   ```bash
   kubectl get volumereplicationclass
   ```

1. **Labels on VolumeReplicationClass:**

   ```bash
   kubectl get volumereplicationclass -o yaml | grep "ramendr.openshift.io/replicationid"
   ```

**Solution:** Ensure storage provider has created classes with appropriate
Ramen labels.

### S3 Not Reachable

**Symptom:** `Reachable` condition is `False`.

**Check:**

1. **S3 secret exists:**

   ```bash
   kubectl get secret -n ramen-system | grep s3
   ```

1. **S3 credentials are correct:**

   ```bash
   kubectl get secret <s3-secret-name> -n ramen-system -o yaml
   ```

1. **Network connectivity to S3:**

   ```bash
   # Test from a pod
   kubectl run -it --rm debug --image=amazon/aws-cli --restart=Never -- \
     s3 ls --endpoint-url=https://s1.amazonaws.com s3://<bucket-name>
   ```

**Solution:** Verify S3 configuration in DRCluster and ensure network
policies allow S3 access.

### Replication Schedules Not Applied

**Symptom:** VolumeReplicationClass with requested schedule doesn't exist.

**Check:**

1. **DRClusterConfig spec has schedules:**

   ```bash
   kubectl get drclusterconfig drclusterconfig -o jsonpath='{.spec.replicationSchedules}'
   ```

1. **Storage provider controller is running:**

   ```bash
   kubectl get pods -n rook-ceph | grep operator
   ```

1. **Storage provider logs:**

   ```bash
   kubectl logs -n rook-ceph deploy/rook-ceph-operator | grep VolumeReplicationClass
   ```

**Solution:** Ensure storage provider controller is watching
DRClusterConfig and creating classes.

### ClusterID Mismatch

**Symptom:** Peer classes not matching across clusters.

**Check:**

```bash
# Verify clusterID matches kube-system namespace UID
kubectl get namespace kube-system -o jsonpath='{.metadata.uid}'
kubectl get drclusterconfig drclusterconfig -o jsonpath='{.spec.clusterID}'
```

**Solution:** ClusterID should automatically match namespace UID. If not,
check Ramen operator logs.

## Best Practices

1. **Don't manually edit DRClusterConfig** - It's managed by Ramen operators

1. **Monitor status regularly** - Ensure classes are being discovered:

   ```bash
   kubectl get drclusterconfig drclusterconfig -o jsonpath='{.status}' | jq
   ```

1. **Label storage resources correctly:**

    - StorageClass: `ramendr.openshift.io/storageid: <unique-id>`
    - VolumeReplicationClass: `ramendr.openshift.io/replicationid: <unique-id>`

1. **Test S3 connectivity** before creating DRClusters on the hub

1. **Ensure storage providers are running** and watching DRClusterConfig

1. **Check for consistent labels** across peer clusters for proper class matching

## Storage Provider Guidelines

If you're developing a storage provider that integrates with Ramen:

1. **Watch DRClusterConfig resources:**

   ```go
   // Watch for DRClusterConfig changes
   err := controller.Watch(&source.Kind{Type: &ramendrv1alpha1.DRClusterConfig{}}, handler)
   ```

1. **Parse replicationSchedules** and create matching VolumeReplicationClass resources

1. **Label your resources:**

   ```yaml
   metadata:
     labels:
       ramendr.openshift.io/replicationid: "your-replication-id"
   ```

1. **Report capabilities** by ensuring Ramen can discover your classes via labels

1. **Handle schedule updates** when DRClusterConfig spec changes

## Related Resources

- [DRCluster](drcluster-crd.md) - Hub-side cluster configuration
- [DRPolicy](drpolicy-crd.md) - Uses peer classes discovered via DRClusterConfig
- [VolumeReplicationGroup](vrg-crd.md) - Consumes VolumeReplicationClass resources
- [Configuration Guide](configure.md) - S3 and storage configuration
