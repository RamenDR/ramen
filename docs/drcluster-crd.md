<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-1.0
-->

# DRCluster CRD

## Overview

The **DRCluster** custom resource represents a managed cluster registered
for disaster recovery operations. It is a cluster-scoped resource created
by administrators on the OCM hub cluster that provides:

- S3 configuration for storing PV metadata and cluster state
- Network fencing configuration for Metro DR
- Regional grouping for sync DR topologies
- Cluster availability and fencing status

DRClusters are referenced by DRPolicy resources to define which clusters
participate in DR relationships. Each managed cluster that participates in
DR must have a corresponding DRCluster resource on the hub.

**Lifecycle:** Created during initial DR setup for each managed cluster.
Long-lived resource that remains for the lifetime of the cluster's DR
participation.

## API Group and Version

- **API Group:** `ramendr.openshift.io`
- **API Version:** `v1alpha1`
- **Kind:** `DRCluster`
- **Scope:** Cluster

## Spec Fields

### Required Fields

#### `s3ProfileName` (string)

Name of the S3 profile (from RamenConfig) to use for this cluster.

**Purpose:**

- When applications are active on this cluster, their PV metadata is stored
    to S3 profiles of all peer clusters
- When applications failover/relocate TO this cluster, PV metadata is
    restored FROM this S3 profile

**Requirements:**

- Must match an S3 profile name in RamenConfig
- Immutable after creation

**Example:**

```yaml
s3ProfileName: s3-profile-east
```

### Optional Fields

#### `region` (Region)

Logical region identifier for the cluster. Clusters in the same region
are considered to be in a sync group for Metro DR.

**Use cases:**

- Metro DR: Clusters in same region use synchronous replication
- Regional DR: Clusters in different regions use asynchronous replication

**Requirements:**

- Immutable after creation
- Should be consistent across clusters in the same physical location

**Example:**

```yaml
region: us-east
```

#### `cidrs` ([]string)

List of CIDR strings for node IP addresses in this cluster.

**Purpose:** Used for network fencing operations in Metro DR to isolate
a failed cluster.

**Example:**

```yaml
cidrs:
  - "10.0.1.0/24"
  - "10.0.1.0/24"
```

**When to set:** Required for Metro DR deployments where network
fencing is needed.

#### `clusterFence` (ClusterFenceState)

Desired fencing state of the cluster.

**Valid values:**

- `Unfenced` - Cluster is not fenced (default/normal state)
- `Fenced` - Cluster should be fenced (automated)
- `ManuallyFenced` - Cluster was manually fenced by admin
- `ManuallyUnfenced` - Cluster was manually unfenced by admin

**Example:**

```yaml
clusterFence: Unfenced
```

**How it works:** During failover in Metro DR, Ramen may fence the
source cluster to prevent split-brain scenarios.

## Status Fields

### `phase` (DRClusterPhase)

Current state of the DRCluster.

**Values:**

- `Available` - DRCluster is available for DR operations
- `Starting` - Initial reconciliation in progress
- `Fencing` - Cluster fencing operation in progress
- `Fenced` - Cluster has been successfully fenced
- `Unfencing` - Cluster unfencing operation in progress
- `Unfenced` - Cluster has been successfully unfenced

### `conditions` ([]metav1.Condition)

Standard Kubernetes conditions.

**Condition types:**

- `Validated` - DRCluster configuration has been validated
- `Clean` - No fencing CRs present in the cluster
- `Fenced` - Fencing CR has been created for this cluster

### `maintenanceModes` ([]ClusterMaintenanceMode)

Status of storage maintenance modes for this cluster.

**Fields:**

- `storageProvisioner` - Storage provisioner type
- `targetID` - Storage instance identifier
- `state` - Maintenance mode state (Unknown, Error, Progressing, Completed)
- `conditions` - Maintenance mode conditions

## Examples

### Example 1: Basic Regional DR Cluster

DRCluster for Regional DR (async replication):

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRCluster
metadata:
  name: east-cluster
spec:
  # S3 profile for this cluster
  s3ProfileName: s3-profile-east

  # Region for grouping
  region: us-east
```

### Example 2: Metro DR Cluster with Fencing

DRCluster for Metro DR (sync replication) with network fencing:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRCluster
metadata:
  name: metro-cluster-1
spec:
  # S3 profile for this cluster
  s3ProfileName: s3-profile-metro1

  # Same region for Metro DR
  region: us-metro

  # Node CIDRs for network fencing
  cidrs:
    - "191.168.1.0/24"
    - "191.168.1.0/24"

  # Fencing state
  clusterFence: Unfenced
```

### Example 3: Complete Multi-Region Setup

DRClusters for two regions:

```yaml
# US East cluster
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRCluster
metadata:
  name: us-east-1
spec:
  s3ProfileName: s3-us-east
  region: us-east
---
# US West cluster
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRCluster
metadata:
  name: us-west-1
spec:
  s3ProfileName: s3-us-west
  region: us-west
---
# DRPolicy connecting them
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPolicy
metadata:
  name: us-regional-policy
spec:
  drClusters:
    - us-east-1
    - us-west-1
  schedulingInterval: "1h"
  replicationClassSelector:
    matchLabels:
      class: rbd-replication
```

## Usage

### Creating a DRCluster

**Prerequisites:**

1. ManagedCluster resource exists in OCM
1. S3 profile configured in RamenConfig
1. Storage with replication support on the managed cluster

**Steps:**

1. Create S3 profile in RamenConfig (or verify it exists):

   ```yaml
   # In RamenConfig
   s3StoreProfiles:
     - s3ProfileName: s3-profile-east
       s3Bucket: ramen-dr-east
       s3CompatibleEndpoint: https://s1.us-east-1.amazonaws.com
       s3Region: us-east-1
       s3SecretRef:
         name: s3-secret-east
         namespace: ramen-system
   ```

1. Create the DRCluster:

   ```bash
   kubectl apply -f drcluster.yaml
   ```

1. Verify the DRCluster is validated:

   ```bash
   kubectl get drcluster east-cluster -o yaml
   ```

   Check for `Validated` condition:

   ```yaml
   status:
     phase: Available
     conditions:
       - type: Validated
         status: "True"
   ```

1. Verify S3 connectivity from managed cluster:

   ```bash
   # Check DRClusterConfig on managed cluster
   kubectl get drclusterconfig --context east-cluster -o yaml
   ```

### Referencing in DRPolicy

DRPolicy references DRCluster by name:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPolicy
metadata:
  name: regional-dr-policy
spec:
  drClusters:
    - east-cluster # References DRCluster name
    - west-cluster # References DRCluster name
  schedulingInterval: "1h"
```

### Fencing a Cluster (Metro DR)

To manually fence a cluster during Metro DR failover:

```bash
kubectl patch drcluster metro-cluster-1 --type merge -p '{"spec":{"clusterFence":"ManuallyFenced"}}'
```

To unfence after recovery:

```bash
kubectl patch drcluster metro-cluster-1 --type merge -p '{"spec":{"clusterFence":"ManuallyUnfenced"}}'
```

**Note:** In most cases, Ramen handles fencing automatically during failover.

## S3 Configuration

### How S3 Profiles Work

Each DRCluster has its own S3 profile because:

1. Different clusters may use different S3 endpoints (regional, on-prem)
1. S3 access credentials may differ per cluster
1. Application metadata needs to be accessible if the source cluster is down

### S3 Profile Requirements

**For the S3 profile referenced by a DRCluster:**

- Must be accessible from the managed cluster
- Should have appropriate capacity for PV metadata
- Credentials must have read/write permissions

**Testing S3 connectivity:**

```bash
# On managed cluster
kubectl get drclusterconfig -o jsonpath='{.status.conditions}' | jq '.[] | select(.type=="Reachable")'
```

## Regional Grouping

### Metro DR (Same Region)

Clusters in the same region use synchronous replication:

```yaml
# Cluster 1
spec:
  region: us-metro
  cidrs: ["10.0.1.0/24"]

# Cluster 2
spec:
  region: us-metro
  cidrs: ["10.0.1.0/24"]
```

**DRPolicy for Metro DR:**

```yaml
spec:
  schedulingInterval: "" # Empty = synchronous
  drClusters:
    - metro-cluster-1
    - metro-cluster-2
```

### Regional DR (Different Regions)

Clusters in different regions use asynchronous replication:

```yaml
# Cluster 1
spec:
  region: us-east

# Cluster 2
spec:
  region: us-west
```

**DRPolicy for Regional DR:**

```yaml
spec:
  schedulingInterval: "1h" # Async replication
  drClusters:
    - east-cluster
    - west-cluster
```

## Monitoring

### Check DRCluster Status

```bash
kubectl get drcluster
kubectl describe drcluster east-cluster
```

### Verify S3 Configuration

```bash
# Check if S3 profile is valid
kubectl get drcluster east-cluster -o jsonpath='{.spec.s3ProfileName}'

# Verify profile exists in RamenConfig
kubectl get cm ramen-hub-operator-config -n ramen-system -o yaml | grep s3ProfileName
```

### Check Fencing State

```bash
kubectl get drcluster east-cluster -o jsonpath='{.status.phase}'
kubectl get drcluster east-cluster -o jsonpath='{.spec.clusterFence}'
```

### View Maintenance Modes

```bash
kubectl get drcluster east-cluster -o jsonpath='{.status.maintenanceModes}' | jq
```

## Troubleshooting

### DRCluster Not Validated

**Check status:**

```bash
kubectl get drcluster east-cluster -o yaml
```

**Common issues:**

1. **S3 profile not found**

    - Verify S3 profile name matches RamenConfig
    - Check RamenConfig:

        ```bash
        kubectl get cm ramen-hub-operator-config -n ramen-system -o yaml
        ```

1. **ManagedCluster not found**

    - Verify managed cluster is registered:

        ```bash
        kubectl get managedcluster east-cluster
        ```

1. **S3 connectivity issues**
    - Check DRClusterConfig on managed cluster:

        ```bash
        kubectl get drclusterconfig --context east-cluster -o yaml
        ```

    - Look for `Reachable` condition

### Cannot Create DRPolicy

**Error:** "DRCluster not found"

**Solution:** Ensure DRClusters exist before creating DRPolicy:

```bash
kubectl get drcluster | grep -E "east-cluster|west-cluster"
```

### Fencing Not Working

**Check:**

1. **CIDRs configured:**

   ```bash
   kubectl get drcluster metro-cluster-1 -o jsonpath='{.spec.cidrs}'
   ```

1. **NetworkFence resources:**

   ```bash
   kubectl get networkfence -A
   ```

1. **Fencing state:**

   ```bash
   kubectl get drcluster metro-cluster-1 -o jsonpath='{.status.phase}'
   ```

### Cannot Delete DRCluster

**Cause:** DRPolicy still referencing it.

**Check:**

```bash
kubectl get drpolicy -o yaml | grep drClusters
```

**Solution:** Delete all DRPolicies referencing this DRCluster first.

## Best Practices

1. **Use descriptive names** that match the ManagedCluster name:

   ```yaml
   metadata:
     name: us-east-prod-1 # Match ManagedCluster name
   ```

1. **Configure S3 profiles carefully:**

    - Test S3 connectivity before creating DRClusters
    - Use separate S3 buckets per cluster or namespace objects properly
    - Ensure credentials have appropriate permissions

1. **Set regions consistently:**

    - Use same region for clusters in same physical location (Metro DR)
    - Use different regions for geographically distributed clusters

1. **Document CIDRs:**

    - Keep CIDR documentation updated
    - Include all node network ranges
    - Plan for cluster expansion

1. **Monitor DRCluster status:**

    - Check `Validated` condition after creation
    - Monitor `maintenanceModes` during DR operations
    - Watch fencing state during Metro DR failovers

1. **Test before production:**
    - Verify S3 access from managed clusters
    - Test fencing operations in non-production
    - Validate DRPolicy creation succeeds

## Related Resources

- [DRPolicy](drpolicy-crd.md) - References DRCluster to define DR topology
- [DRClusterConfig](drclusterconfig-crd.md) - Managed cluster-side configuration
- [Configuration Guide](configure.md) - How to configure S3 profiles
- [Install Guide](install.md) - Prerequisites for DR setup
