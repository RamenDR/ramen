<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-1.0
-->

# Workload Management Using Ramen

This guide describes how to protect and manage workloads
using Ramen's disaster recovery capabilities.

## Overview

Ramen provides three methods to protect applications across clusters:

1. **GitOps-based Protection** (Recommended) - For
    applications deployed via ArgoCD ApplicationSets
1. **Discovered Applications** - For existing applications
    deployed via kubectl, Helm, or other methods
1. **Recipe-based Protection** - For complex stateful
    applications requiring custom capture/recovery workflows

All methods support the same DR operations:

- **Relocate**: Planned migration to a peer cluster
- **Failover**: Unplanned recovery due to cluster failure
- **Failback**: Return to the original cluster after recovery

## Prerequisites

Before protecting workloads with Ramen, ensure:

1. **Ramen is installed** - See [install guide](install.md)
1. **Ramen is configured** - See [configure guide](configure.md)
1. **DRPolicy is created** - Defines the DR topology and peer clusters
1. **Storage replication is configured** - CSI storage with
    replication support (e.g., Ceph)
1. **S3 object storage is available** - For storing VRG and PV metadata

## Key Concepts

### DRPolicy

Defines the disaster recovery topology and replication
configuration between peer clusters.

**Example:**

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPolicy
metadata:
  name: dr-policy
spec:
  schedulingInterval: "1h" # Replication schedule (async DR)
  drClusters:
    - east-cluster
    - west-cluster
  replicationClassSelector:
    matchLabels:
      class: ramen-replication-class
  volumeSnapshotClassSelector:
    matchLabels:
      class: ramen-snapshot-class
```

**Key fields:**

- `schedulingInterval`: How often to replicate (e.g., "1h",
    "30m"). Omit for synchronous DR.
- `drClusters`: List of peer cluster names participating in DR
- `replicationClassSelector`: Selects VolumeReplicationClass for async replication
- `volumeSnapshotClassSelector`: Selects VolumeSnapshotClass for sync replication

### DRPlacementControl (DRPC)

Orchestrates DR operations for a specific application. The DRPC references:

- A DRPolicy (defines where the app can run)
- A Placement resource (OCM Placement or PlacementRule)
- PVC selector (which volumes to protect)

**Example:**

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: my-app-drpc
  namespace: my-app-namespace
spec:
  preferredCluster: east-cluster
  drPolicyRef:
    name: dr-policy
  placementRef:
    kind: Placement
    name: my-app-placement
  pvcSelector:
    matchLabels:
      app: my-app
```

**Key fields:**

- `preferredCluster`: Initial cluster where the application should run
- `drPolicyRef`: Reference to the DRPolicy
- `placementRef`: Reference to OCM Placement/PlacementRule
- `pvcSelector`: Label selector to identify PVCs to protect

### VolumeReplicationGroup (VRG)

Created automatically by the DRPC on managed clusters. The VRG manages:

- Volume replication for PVCs
- Kube object protection (application resources)
- Primary/Secondary state coordination

Users typically don't create VRGs directly - they are managed by the DRPC.

## Protection Methods

### Method 1: GitOps-based Protection (Recommended)

Best for applications deployed using ArgoCD ApplicationSets.
The Git repository serves as the source of truth.

**Workflow:**

1. Deploy your application via ArgoCD ApplicationSet
1. Create a DRPlacementControl referencing the Placement
1. Ramen automatically protects PVCs and syncs application state

**Example for ArgoCD ApplicationSet:**

```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: my-app
  namespace: argocd
spec:
  generators:
    - clusterDecisionResource:
        configMapRef: ocm-placement-generator
        labelSelector:
          matchLabels:
            cluster.open-cluster-management.io/placement: my-app-placement
  template:
    metadata:
      name: my-app
    spec:
      project: default
      source:
        repoURL: https://github.com/example/my-app
        targetRevision: main
        path: deploy
      destination:
        namespace: my-app
        name: "{{name}}"
```

Then create the DRPC:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: my-app-drpc
  namespace: my-app
spec:
  preferredCluster: east-cluster
  drPolicyRef:
    name: dr-policy
  placementRef:
    kind: Placement
    name: my-app-placement
  pvcSelector:
    matchLabels:
      app: my-app
```

**Advantages:**

- Declarative and version-controlled
- Automatic application recreation from Git
- Industry best practice for cloud-native apps

### Method 2: Discovered Applications

Protects existing applications without GitOps. Ramen
discovers and captures all Kubernetes resources.

**Workflow:**

1. Deploy your application using any method (kubectl, Helm, etc.)
1. Label your PVCs for protection
1. Create a DRPlacementControl with kubeObjectProtection enabled

**Example:**

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: my-app-drpc
  namespace: my-app
spec:
  preferredCluster: east-cluster
  drPolicyRef:
    name: dr-policy
  placementRef:
    kind: PlacementRule
    name: my-app-placement
  pvcSelector:
    matchLabels:
      app: my-app
  kubeObjectProtection:
    captureInterval: 5m
```

**How it works:**

- Ramen captures all resources in the namespace via Velero
- Resources are stored in S3 object storage
- During recovery, resources are restored from S3

**Advantages:**

- Works with any deployment method
- No code or deployment changes required
- Good for legacy applications

### Method 3: Recipe-based Protection

For complex stateful applications requiring custom workflows (e.g., databases, middleware).

**Use cases:**

- Applications needing quiesce operations before backup
- Specific resource ordering during capture/recovery
- Custom pre/post hooks for DR operations
- Vendor-supplied DR specifications

**Workflow:**

1. Create a Recipe defining capture and recovery workflows
1. Create a VRG or DRPC referencing the Recipe
1. Ramen executes the Recipe workflows during DR operations

**Example Recipe:**

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: Recipe
metadata:
  name: mysql-recipe
  namespace: mysql-app
spec:
  appType: mysql
  groups:
    - name: config
      type: resource
      includedResourceTypes:
        - configmap
        - secret
    - name: database
      type: resource
      includedResourceTypes:
        - statefulset
        - service
    - name: volumes
      type: volume
      labelSelector: app=mysql
  hooks:
    - name: db-hooks
      labelSelector: app=mysql
      ops:
        - name: pre-backup
          container: mysql
          command: ["/scripts/quiesce.sh"]
          timeout: 300
        - name: post-restore
          container: mysql
          command: ["/scripts/verify.sh"]
          timeout: 600
  workflows:
    - name: capture
      sequence:
        - hook: db-hooks/pre-backup
        - group: config
        - group: volumes
        - group: database
    - name: recover
      sequence:
        - group: config
        - group: volumes
        - group: database
        - hook: db-hooks/post-restore
```

**Reference the Recipe in your VRG:**

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: VolumeReplicationGroup
metadata:
  name: mysql-vrg
  namespace: mysql-app
spec:
  kubeObjectProtection:
    recipeRef:
      name: mysql-recipe
      captureWorkflowName: capture
      recoverWorkflowName: recover
      volumeGroupName: volumes
```

**For detailed Recipe documentation, see [recipe.md](recipe.md).**

**Advantages:**

- Application-specific protection logic
- Ensures data consistency
- Vendor-supported DR workflows
- Custom hook execution

## Common DR Operations

### Relocate (Planned Migration)

Move an application to a peer cluster during maintenance or optimization.

**Steps:**

1. Update the DRPC action field:

   ```bash
   kubectl patch drpc my-app-drpc -n my-app --type merge -p '{"spec":{"action":"Relocate","failoverCluster":"west-cluster"}}'
   ```

1. Monitor the status:

   ```bash
   kubectl get drpc my-app-drpc -n my-app -o yaml
   ```

1. Verify application is running on the target cluster:

   ```bash
   kubectl get pods -n my-app --context west-cluster
   ```

**What happens:**

- Application is gracefully stopped on the source cluster
- Final data replication is performed
- Application starts on the target cluster
- Replication direction is reversed

### Failover (Unplanned Recovery)

Recover an application on a peer cluster due to source cluster failure.

**Steps:**

1. Trigger failover:

   ```bash
   kubectl patch drpc my-app-drpc -n my-app --type merge -p '{"spec":{"action":"Failover","failoverCluster":"west-cluster"}}'
   ```

1. Monitor recovery:

   ```bash
   kubectl get drpc my-app-drpc -n my-app -w
   ```

**What happens:**

- Ramen assumes the source cluster is unavailable
- Latest replicated data is used for recovery
- Application starts on the target cluster
- No final sync from source (data loss possible)

### Failback (Return to Original)

Return the application to the original cluster after recovery.

**Steps:**

1. Ensure original cluster is healthy
1. Trigger failback (same as relocate):

   ```bash
   kubectl patch drpc my-app-drpc -n my-app --type merge -p '{"spec":{"action":"Relocate","failoverCluster":"east-cluster"}}'
   ```

## Monitoring DR Protection

### Check DRPC Status

```bash
kubectl get drpc -A
kubectl describe drpc my-app-drpc -n my-app
```

Look for:

- `status.phase`: Current phase (e.g., Deployed, Relocating, FailedOver)
- `status.conditions`: Detailed condition messages
- `status.lastGroupSyncTime`: Last successful data sync

### Check VRG Status on Managed Clusters

```bash
kubectl get vrg -A --context dr1
kubectl describe vrg my-app-drpc -n my-app --context dr1
```

Look for:

- `status.state`: Primary or Secondary
- `status.conditions`: Replication health
- `status.protectedPVCs`: List of protected volumes

### Check Data Replication

For async replication using VolumeReplication:

```bash
kubectl get volumereplication -A --context dr1
```

For sync replication using VolSync:

```bash
kubectl get replicationsource,replicationdestination -A --context dr1
```

## Troubleshooting

### Application not failing over

**Check:**

1. DRPC action is set correctly
1. Target cluster is registered in DRPolicy
1. VRG on source shows replication is healthy
1. S3 storage is accessible from target cluster

### Data not replicating

**Check:**

1. VolumeReplicationClass or VolumeSnapshotClass exists
1. Storage backend supports replication (e.g., Ceph RBD mirroring configured)
1. VolumeReplication resources are bound
1. Check VR/VS status for errors

### Kube objects not restoring

**Check:**

1. Velero is installed on all clusters (for discovered apps)
1. Recipe workflows are valid (for recipe-based apps)
1. S3 bucket contains backup data
1. Check VRG events for capture/restore errors

## Next Steps

- **For development testing**: See [user-quick-start.md]
    user-quick-start.md) to set up a test environment
- **For Recipe details**: See [recipe.md](recipe.md) for advanced Recipe-based protection
- **For operational guidance**: See [configure.md](configure.md) for production configuration
- **For metrics**: See [metrics.md](metrics.md) to monitor Ramen operations
