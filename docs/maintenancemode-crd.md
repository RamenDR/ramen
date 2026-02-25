<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-1.0
-->

# MaintenanceMode CRD

## Overview

The **MaintenanceMode** custom resource enables coordination between Ramen
and storage backends during disaster recovery operations. It is a
cluster-scoped resource created by Ramen on managed clusters to signal
storage providers that specific maintenance actions are required before
proceeding with DR operations.

Storage backends use MaintenanceMode to:

- Prepare storage systems for failover operations
- Perform necessary cleanup or synchronization
- Report readiness back to Ramen

This allows storage providers to integrate custom logic into Ramen's DR
workflows without Ramen needing to know storage-specific implementation
details.

**Lifecycle:** Automatically created by Ramen VRG controller when
maintenance mode is required. Reconciled by storage provider controllers.
Deleted when maintenance is complete.

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

## How It Works

### Workflow

1. **VRG Detects Maintenance Mode Requirement:**

    - During failover, VRG checks PVC storage identifiers
    - Finds `ramendr.openshift.io/maintenancemodes: Failover` label on
        StorageClass or VolumeReplicationClass
    - Extracts `storageProvisioner` and `targetID` from labels

1. **Ramen Creates MaintenanceMode:**

    - Creates MaintenanceMode resource with appropriate spec
    - Waits for `state: Completed` before proceeding

1. **Storage Provider Reconciles:**

    - Controller watches MaintenanceMode with matching `storageProvisioner`
    - Identifies the specific backend via `targetID`
    - Performs necessary storage operations (e.g., quiesce, flush, fence)
    - Updates status to `Progressing` → `Completed`

1. **Ramen Proceeds:**
    - Detects `state: Completed` and `observedGeneration` matches
    - Continues with failover operation
    - Eventually deletes MaintenanceMode after DR operation completes

### Storage Provider Integration

Storage providers indicate maintenance mode requirements via labels:

**On StorageClass:**

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: ceph-rbd
  labels:
    ramendr.openshift.io/storageid: ceph-cluster-east
    ramendr.openshift.io/maintenancemodes: Failover
provisioner: rbd.csi.ceph.com
```

**On VolumeReplicationClass:**

```yaml
apiVersion: replication.storage.openshift.io/v1alpha1
kind: VolumeReplicationClass
metadata:
  name: rbd-replication
  labels:
    ramendr.openshift.io/replicationid: ceph-replication
    ramendr.openshift.io/maintenancemodes: Failover
spec:
  provisioner: rbd.csi.ceph.com
```

## Usage

### Viewing MaintenanceMode Resources

**On managed cluster:**

```bash
kubectl get maintenancemode
kubectl describe maintenancemode <name>
```

### Checking Status

```bash
# Check current state
kubectl get maintenancemode <name> -o jsonpath='{.status.state}'

# Check conditions
kubectl get maintenancemode <name> -o jsonpath='{.status.conditions}' | jq
```

### Monitoring During DR Operations

```bash
# Watch MaintenanceMode creation/deletion during failover
kubectl get maintenancemode -w

# Check DRCluster for maintenance mode status
kubectl get drcluster <cluster-name> -o jsonpath='{.status.maintenanceModes}' | jq
```

## Monitoring

### Check MaintenanceMode State

```bash
kubectl get maintenancemode
```

Output shows: NAME, STATE, AGE

### View Detailed Status

```bash
kubectl describe maintenancemode <name>
```

### Monitor from Hub

DRCluster status on hub reflects maintenance modes:

```bash
kubectl get drcluster east-cluster -o jsonpath='{.status.maintenanceModes}' | jq
```

Example output:

```json
[
  {
    "storageProvisioner": "rbd.csi.ceph.com",
    "targetID": "ceph-cluster-east",
    "state": "Completed",
    "conditions": [
      {
        "type": "FailoverActivated",
        "status": "True"
      }
    ]
  }
]
```

## Troubleshooting

### MaintenanceMode Stuck in Progressing

**Symptom:** `state: Progressing` for extended period, failover not completing.

**Check:**

1. **Storage provider controller is running:**

   ```bash
   kubectl get pods -n <storage-namespace> | grep operator
   ```

1. **Controller logs:**

   ```bash
   kubectl logs -n <storage-namespace> <controller-pod> | grep MaintenanceMode
   ```

1. **MaintenanceMode details:**

   ```bash
   kubectl describe maintenancemode <name>
   ```

**Common causes:**

- Storage provider controller not watching MaintenanceMode
- TargetID doesn't match any managed storage backend
- Storage backend unreachable or unhealthy

**Solution:** Check storage provider integration and backend health.

### MaintenanceMode in Error State

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

### VRG Waiting for Maintenance Mode

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

### MaintenanceMode Not Created

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

## Best Practices

1. **Don't create MaintenanceMode manually** - Let VRG controller manage them

1. **Storage providers should watch MaintenanceMode:**

   ```go
   // Example controller watch
   err := controller.Watch(
       &source.Kind{Type: &ramendrv1alpha1.MaintenanceMode{}},
       handler.EnqueueRequestsFromMapFunc(storageProviderMapFunc),
   )
   ```

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

## Storage Provider Implementation Guide

If you're implementing a storage provider that needs maintenance mode integration:

### 1. Label Your Storage Classes

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: your-storage-class
  labels:
    ramendr.openshift.io/storageid: your-storage-id
    ramendr.openshift.io/maintenancemodes: Failover # Indicate you need maintenance
provisioner: your.csi.driver
```

### 1. Watch MaintenanceMode Resources

```go
// Filter for your provisioner
func (r *YourReconciler) filterMaintenanceMode(obj client.Object) bool {
    mmode := obj.(*ramendrv1alpha1.MaintenanceMode)
    return mmode.Spec.StorageProvisioner == "your.csi.driver"
}
```

### 1. Reconcile MaintenanceMode

```go
func (r *YourReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    mmode := &ramendrv1alpha1.MaintenanceMode{}
    if err := r.Get(ctx, req.NamespacedName, mmode); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // Check if this is for your backend
    if mmode.Spec.TargetID != "your-backend-id" {
        return ctrl.Result{}, nil
    }

    // Set progressing
    mmode.Status.State = ramendrv1alpha1.MModeStateProgressing
    if err := r.Status().Update(ctx, mmode); err != nil {
        return ctrl.Result{}, err
    }

    // Perform storage-specific maintenance actions
    if err := r.prepareStorageForFailover(mmode.Spec.TargetID); err != nil {
        // Set error state
        mmode.Status.State = ramendrv1alpha1.MModeStateError
        // ... set condition
        return ctrl.Result{}, r.Status().Update(ctx, mmode)
    }

    // Set completed
    mmode.Status.State = ramendrv1alpha1.MModeStateCompleted
    mmode.Status.ObservedGeneration = mmode.Generation
    // ... set FailoverActivated condition to True
    return ctrl.Result{}, r.Status().Update(ctx, mmode)
}
```

### 1. Handle Modes

```go
for _, mode := range mmode.Spec.Modes {
    switch mode {
    case ramendrv1alpha1.MModeFailover:
        // Prepare for failover
        if err := r.prepareFailover(); err != nil {
            return err
        }
    // Handle future modes
    }
}
```

## Related Resources

- [VolumeReplicationGroup](vrg-crd.md) - Creates MaintenanceMode during DR operations
- [DRCluster](drcluster-crd.md) - Reports maintenance mode status to hub
- [DRClusterConfig](drclusterconfig-crd.md) - Advertises available storage capabilities
