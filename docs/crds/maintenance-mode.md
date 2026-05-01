<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# MaintenanceMode CRD

## Overview

Cluster-scoped resource created by Ramen on managed clusters to
coordinate with storage backends during DR operations. Storage
backends reconcile this resource to prepare for failover and report
readiness back to Ramen.

**Lifecycle:** Automatically created by Ramen when maintenance mode is
required. Reconciled by storage provider controllers. Deleted when
maintenance is complete.

See [Maintenance Mode](../maintenance-mode.md) for the full feature
documentation.

## API Group and Version

- **API Group:** `ramendr.openshift.io`
- **API Version:** `v1alpha1`
- **Kind:** `MaintenanceMode`
- **Scope:** Cluster (on managed clusters)

## Spec Fields

### Required Fields

#### `storageProvisioner` (string)

The storage provisioner type that should handle this maintenance mode
request.

**Value:** Must match the provisioner string in the StorageClass being used.

**Example:**

```yaml
storageProvisioner: rbd.csi.ceph.com
```

**Purpose:** Storage provider controllers watch for MaintenanceMode
resources where `storageProvisioner` matches their provisioner.

#### `targetID` (string)

The storage or replication instance identifier for the specific
storage backend.

**Source:** Read from the `ramendr.openshift.io/storageid` or
`ramendr.openshift.io/replicationid` label on StorageClass or VolumeReplicationClass.

**Example:**

```yaml
targetID: ceph-cluster-1
```

**Purpose:** Allows storage providers managing multiple
backends to identify which instance needs the maintenance action.

#### `modes` ([]MMode)

List of maintenance modes that need to be activated.

**Currently supported values:**

- `Failover` - Storage backend should prepare for failover operation

**Example:**

```yaml
modes:
  - Failover
```

**Future modes:** Additional modes may be added
(e.g., Relocate, Backup, etc.)

## Status Fields

The MaintenanceMode status is populated by the storage provider controller.

### `state` (MModeState)

Current state of the maintenance mode activation.

**Valid values:**

- `Unknown` - Initial state or status unknown
- `Progressing` - Maintenance mode activation in progress
- `Completed` - Maintenance mode successfully activated
- `Error` - Error occurred during activation

**Example:**

```yaml
status:
  state: Completed
```

### `observedGeneration` (int64)

The generation of the MaintenanceMode spec that was last
processed by the storage provider.

**Use:** Ramen compares this with `metadata.generation`
to verify the storage provider has processed the latest request.

### `conditions` ([]metav1.Condition)

Standard Kubernetes conditions providing detailed status.

**Condition types:**

- `FailoverActivated` - Indicates failover maintenance mode has
    been successfully activated

**Example:**

```yaml
status:
  conditions:
    - type: FailoverActivated
      status: "True"
      reason: FailoverPrepared
      message: "Storage backend prepared for failover"
      lastTransitionTime: "2024-01-15T10:30:00Z"
```

## Examples

### Example 1: Failover Maintenance Mode

MaintenanceMode created by Ramen during failover:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: MaintenanceMode
metadata:
  name: mmode-ceph-rbd-failover
spec:
  storageProvisioner: rbd.csi.ceph.com
  targetID: ceph-cluster-east
  modes:
    - Failover
status:
  state: Progressing
  observedGeneration: 1
  conditions:
    - type: FailoverActivated
      status: "False"
      reason: Progressing
      message: "Activating failover maintenance mode"
```

### Example 2: Completed Maintenance Mode

After storage provider has completed preparation:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: MaintenanceMode
metadata:
  name: mmode-ceph-rbd-failover
  generation: 1
spec:
  storageProvisioner: rbd.csi.ceph.com
  targetID: ceph-cluster-east
  modes:
    - Failover
status:
  state: Completed
  observedGeneration: 1
  conditions:
    - type: FailoverActivated
      status: "True"
      reason: Completed
      message: "Failover maintenance mode activated successfully"
      lastTransitionTime: "2024-01-15T10:35:00Z"
```

### Example 3: Error State

When storage provider encounters an error:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: MaintenanceMode
metadata:
  name: mmode-ceph-rbd-failover
spec:
  storageProvisioner: rbd.csi.ceph.com
  targetID: ceph-cluster-east
  modes:
    - Failover
status:
  state: Error
  observedGeneration: 1
  conditions:
    - type: FailoverActivated
      status: "False"
      reason: BackendError
      message: "Failed to contact storage backend: connection timeout"
      lastTransitionTime: "2024-01-15T10:35:00Z"
```

For usage, troubleshooting, and storage provider integration, see
[Maintenance Mode](../maintenance-mode.md).

## Related Resources

- [VolumeReplicationGroup](volume-replication-group.md) - Creates
  MaintenanceMode during DR operations
- [DRCluster](dr-cluster.md) - Reports maintenance mode status to hub
- [DRClusterConfig](dr-cluster-config.md) - Advertises available storage capabilities
