<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Workload Management Using Ramen

This guide describes how to protect and manage workloads using Ramen's
disaster recovery capabilities.

## Overview

Ramen supports protecting applications across three different deployment types:

- GitOps Applications (Recommended) - For applications deployed from Git
  repositories via ArgoCD ApplicationSets
- Discovered Applications - For existing applications deployed via kubectl,
  Helm, or other methods
- Applications with Recipes - For complex stateful applications requiring
  custom capture/recovery workflows

All methods support the same DR operations:

- Relocate: Planned migration to a peer cluster
- Failover: Unplanned recovery due to cluster failure

## Prerequisites

Before protecting workloads with Ramen, ensure:

- Ramen is installed - See [install guide](install.md)
- Ramen is configured - See [configure guide](configure.md)
- DRPolicy is created - Defines the DR topology and peer clusters
- Storage replication is configured - CSI storage driver with
  VolumeReplication support (e.g., Ceph CSI)
- S3 object storage is available - For storing VRG and PV metadata

## Key Concepts

**Note:** All Ramen resources use the API group
`ramendr.openshift.io/v1alpha1`, which is the upstream API group for the
Ramen project.

### DRPlacementControl (DRPC)

Orchestrates DR operations for a specific application. The DRPC references:

- A DRPolicy (defines where the app can run)
- A Placement resource (Open Cluster Management Placement or PlacementRule)
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
- `placementRef`: Reference to Open Cluster Management Placement/PlacementRule
- `pvcSelector`: Label selector to identify PVCs to protect

### VolumeReplicationGroup (VRG)

VRGs are automatically created and managed by the DRPC on managed clusters
to handle volume replication and application resource protection.

## Application Deployment Types

### GitOps Applications (Recommended)

For applications deployed from Git repositories using ArgoCD ApplicationSets.
The Git repository serves as the source of truth for application configuration
and deployment.

**Workflow:**

1. Deploy your application via ArgoCD ApplicationSet to your clusters
1. Create an Open Cluster Management Placement to define cluster selection
1. Create a DRPlacementControl referencing the Placement and DRPolicy
1. Ramen automatically protects PVCs and manages application state across clusters

**Sample Applications:**

For complete examples of GitOps applications with Ramen DR protection, see
the [applicationset directory](https://github.com/RamenDR/ocm-ramen-samples/tree/main/applicationset)
in the ocm-ramen-samples repository.

The repository provides sample applications including:

- **Deployment samples**: Busybox deployment examples with different storage configurations
- **KubeVirt samples**: Virtual machine examples with different storage configurations
- **Complete DR setup**: DRPlacementControl examples in `dr/` directory

**Example workflow:**

For detailed steps on deploying and enabling DR protection for GitOps
applications, follow the instructions in the
[ocm-ramen-samples README](https://github.com/RamenDR/ocm-ramen-samples/blob/main/README.md).

**Advantages:**

- Declarative and version-controlled
- Automatic application recreation from Git
- Industry best practice for cloud-native apps

### Discovered Applications

Protects existing applications without GitOps. Ramen discovers and captures
all Kubernetes resources.

**Workflow:**

1. Deploy your application using any method (kubectl, Helm, Operator, etc.)
1. Ensure PVCs have appropriate labels for the DRPC selector
1. Create an Open Cluster Management Placement to define cluster selection
1. Create a DRPlacementControl with kubeObjectProtection enabled

**Sample Applications:**

For examples of discovered applications that can be deployed and protected,
see the [workloads directory](https://github.com/RamenDR/ocm-ramen-samples/tree/main/workloads)
in the ocm-ramen-samples repository.

The workloads directory contains sample applications including:

- **Deployment samples**: Basic containerized applications (busybox)
- **KubeVirt VMs**: Virtual machine workloads with persistent storage
    - vm-pvc: PVC based VM
    - vm-dv: DataVolume based VM
    - vm-dvt: DataVolumeTemplate based VM

**Example workflow:**

For detailed steps on deploying discovered applications and enabling DR
protection, follow the instructions in the
[ocm-ramen-samples README](https://github.com/RamenDR/ocm-ramen-samples/blob/main/README.md).

**How it works:**

- Ramen captures Kubernetes resources in the namespace using backup tools
- Application metadata is stored in S3-compatible object storage
- During recovery, resources are restored from the backup storage
- PVCs are recreated and reattached to replicated storage volumes

**Advantages:**

- Works with any deployment method
- No code or deployment changes required
- Good for legacy applications

### Applications with Recipes

For complex stateful applications requiring custom workflows (e.g., databases, middleware).

**Use cases:**

- Applications needing quiesce operations before backup
- Specific resource ordering during capture/recovery
- Custom pre/post hooks for DR operations
- Vendor-supplied DR specifications

**Workflow:**

1. Create a Recipe defining capture and recovery workflows
1. Create a DRPC referencing the Recipe
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

**Reference the Recipe in your DRPC:**

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: mysql-drpc
  namespace: mysql-app
spec:
  preferredCluster: east-cluster
  drPolicyRef:
    name: dr-policy
  placementRef:
    kind: Placement
    name: mysql-placement
  pvcSelector:
    matchLabels:
      app: mysql
  kubeObjectProtection:
    recipeRef:
      name: mysql-recipe
      captureWorkflowName: capture
      recoverWorkflowName: recover
```

For detailed Recipe documentation, see [recipe.md](recipe.md).

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
   kubectl patch drpc my-app-drpc -n my-app-namespace --type merge -p '{"spec":{"action":"Relocate","failoverCluster":"west-cluster"}}'
   ```

1. Monitor the status:

   ```bash
   kubectl get drpc my-app-drpc -n my-app-namespace -o yaml
   ```

1. Verify application is running on the target cluster:

   ```bash
   kubectl get pods -n my-app-namespace --context west-cluster
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
   kubectl patch drpc my-app-drpc -n my-app-namespace --type merge -p '{"spec":{"action":"Failover","failoverCluster":"west-cluster"}}'
   ```

1. Monitor recovery:

   ```bash
   kubectl get drpc my-app-drpc -n my-app-namespace -w
   ```

**What happens:**

- Ramen assumes the source cluster is unavailable
- Latest replicated data is used for recovery
- Application starts on the target cluster
- No final sync from source (data loss possible)

## Monitoring DR Protection

### Check DRPC Status

```bash
kubectl get drpc -A
kubectl describe drpc my-app-drpc -n my-app-namespace
```

Look for:

- `status.phase`: Current phase (e.g., Deployed, Relocating, FailedOver)
- `status.conditions`: Detailed condition messages
- `status.lastGroupSyncTime`: Last successful data sync

## Next Steps

- For development testing: See [user-quick-start.md](user-quick-start.md)
  to set up a test environment
- For Recipe details: See [recipe.md](recipe.md) for advanced Recipe-based protection
- For operational guidance: See [configure.md](configure.md) for production configuration
- For metrics: See [metrics.md](metrics.md) to monitor Ramen operations
