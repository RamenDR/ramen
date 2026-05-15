<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Configure Ramen

This guide describes how to configure Ramen for disaster recovery operations after
installing the Ramen operators. Configuration involves setting up S3 storage profiles,
creating DRCluster resources, and establishing DRPolicy resources to define replication
relationships.

## Prerequisites

Before configuring Ramen, ensure you have completed the installation steps described in
[install.md](install.md). You should have:

1. **OCM (Open Cluster Management)** with at least two managed clusters registered
1. **Ramen hub operator** installed on the OCM hub cluster
1. **Ramen catalog** installed on each managed cluster participating in DR

### Gather Required Information

Before starting configuration, collect the following information:

#### S3 Storage Information

For each S3 bucket to replicate Ramen protected k8s resources, prepare:

- **S3 endpoint URL** (e.g., `https://s3.amazonaws.com` or `https://rgw.example.com`)
- **S3 bucket name** (one bucket per cluster pair)
- **S3 region** (if applicable)
- **Access credentials**:
  - AWS Access Key ID
  - AWS Secret Access Key
- **S3 profile name** (a unique name to identify this S3 configuration within Ramen)

#### Cluster Information

- **Managed cluster names** as registered in OCM (verify with
  `kubectl get managedclusters`)

#### Replication Configuration

- **Replication type**: async (Regional DR) or sync (Metro DR)
- **Scheduling interval** (for async): e.g., `5m`, `30m`, `1h`

## Update Ramen Config with S3 Profiles

Ramen uses S3-compatible object storage to store cluster metadata (PV specs, VRG state,
application specs) for cross-cluster recovery. S3 profiles are configured in the Ramen
hub operator ConfigMap.

### Understanding S3 Profiles

Each managed cluster should have an associated S3 profile that defines where to store
and retrieve its metadata. The S3 profile configuration includes:

- Profile name (referenced in DRCluster resources)
- S3 endpoint, bucket, and region
- Access credentials stored in a Kubernetes Secret

**Important**: S3 profiles are configured on the **hub cluster** in the
`ramen-hub-operator-config` ConfigMap in the `ramen-system` namespace, and are
automatically distributed to the managed clusters that form a DRPolicy.

### Step 1: Create S3 Secrets

Create a Kubernetes Secret containing S3 credentials for each managed cluster.

#### Example: S3 secret for east-cluster

```bash
kubectl create secret generic s3secret-east-cluster \
  --from-literal=AWS_ACCESS_KEY_ID=<access-key-id> \
  --from-literal=AWS_SECRET_ACCESS_KEY=<secret-access-key> \
  -n ramen-system
```

#### Example: S3 secret for west-cluster

```bash
kubectl create secret generic s3secret-west-cluster \
  --from-literal=AWS_ACCESS_KEY_ID=<access-key-id> \
  --from-literal=AWS_SECRET_ACCESS_KEY=<secret-access-key> \
  -n ramen-system
```

#### Verify secrets

```bash
kubectl get secrets -n ramen-system | grep s3secret
```

### Step 2: Update Ramen Hub ConfigMap

The Ramen hub operator configuration is stored in the `ramen-hub-operator-config`
ConfigMap in the `ramen-system` namespace.

#### Retrieve the current ConfigMap

```bash
kubectl get configmap ramen-hub-operator-config -n ramen-system -o yaml \
  > ramen-config.yaml
```

#### Edit the ConfigMap to add S3 profiles

The ConfigMap contains a `ramen_manager_config.yaml` key with the Ramen configuration.
You need to add or update the `s3StoreProfiles` section.

#### Example ConfigMap structure

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: ramen-hub-operator-config
  namespace: ramen-system
data:
  ramen_manager_config.yaml: |
    # Ramen configuration
    apiVersion: ramendr.openshift.io/v1alpha1
    kind: RamenConfig

    # Health check and monitoring
    health:
      healthProbeBindAddress: :8081
    metrics:
      bindAddress: 127.0.0.1:8080

    # Leader election
    leaderElection:
      enabled: true
      resourceName: hub.ramendr.openshift.io

    # Volume replication configuration
    volSync:
      disabled: false

    # S3 storage profiles
    s3StoreProfiles:
      # Profile for east-cluster
      - s3ProfileName: s3-profile-east
        s3Bucket: ramen-bucket-east
        s3Region: us-east-1
        s3CompatibleEndpoint: https://s3.us-east-1.amazonaws.com
        s3SecretRef:
          name: s3secret-east-cluster
          namespace: ramen-system

      # Profile for west-cluster
      - s3ProfileName: s3-profile-west
        s3Bucket: ramen-bucket-west
        s3Region: us-west-2
        s3CompatibleEndpoint: https://s3.us-west-2.amazonaws.com
        s3SecretRef:
          name: s3secret-west-cluster
          namespace: ramen-system
```

#### Apply the updated ConfigMap

```bash
kubectl apply -f ramen-config.yaml
```

#### Restart the Ramen hub operator to apply changes

```bash
kubectl rollout restart deployment/ramen-hub-operator -n ramen-system
```

TODO: Ideally this is not required!

#### Verify the operator restarted successfully

```bash
kubectl get pods -n ramen-system -w
```

Wait for the hub operator pod to reach `Running` state with `2/2` containers ready.

## Create DRCluster Resources

`DRCluster` resources represent managed clusters participating in disaster recovery
operations. Each managed cluster in your DR topology requires a corresponding DRCluster
resource on the hub cluster.

### Understanding DRCluster

A DRCluster resource defines:

- **S3 profile** for storing and retrieving cluster metadata
- **Network CIDRs** for fencing operations (Metro DR)
- **Fencing state** for disaster scenarios

**Important**: DRCluster resources are created on the **hub cluster**, not on the
managed clusters.

### DRCluster Specification

Key fields:

- `s3ProfileName` (Required, Immutable): S3 profile name from Ramen config
- `cidrs` (Optional): Network CIDRs for the managed cluster nodes
- `clusterFence` (Optional): Fencing state (`Unfenced`, `Fenced`, `ManuallyFenced`,
  `ManuallyUnfenced`)

**Note**: The `region` field is deprecated. Ramen now uses StorageClass labels
(`ramendr.openshift.io/storageid`) to automatically discover storage relationships
between clusters.

### Storage Discovery Mechanism

Ramen automatically discovers whether clusters have synchronous or asynchronous
replication relationships, based on the following conditions:

- **Same StorageClass names** across the clusters, backed by the same CSI driver, and
- **Same `storageID`** on StorageClasses across clusters → **Metro DR** (sync
  replication), or
- **Different `storageID`** on StorageClasses across clusters → **Regional DR** (async
  replication)

**Ensure your StorageClasses are properly labeled:**

```bash
# On each managed cluster
kubectl label storageclass <storage-class-name> \
  ramendr.openshift.io/storageid=<storage-identifier>
```

**Example:**

- For Metro DR, use the same `storageid` on both clusters:
  `storagecluster-shared-instance-1`
- For Regional DR, use different `storageID` values: `storagecluster-instance-east` and
  `storagecluster-instance-west`

### Example 1: Regional DR (Async) DRClusters

For asynchronous replication between geographically distributed clusters.

**east-cluster DRCluster:**

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRCluster
metadata:
  name: east-cluster
spec:
  # S3 profile for storing cluster metadata
  s3ProfileName: s3-profile-east

  # Network CIDRs for fencing (optional but recommended)
  cidrs:
    - "10.0.0.0/16"
    - "192.168.1.0/24"

  # Initial fencing state
  clusterFence: Unfenced
```

**west-cluster DRCluster:**

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRCluster
metadata:
  name: west-cluster
spec:
  # S3 profile for storing cluster metadata
  s3ProfileName: s3-profile-west

  # Network CIDRs for fencing
  cidrs:
    - "10.1.0.0/16"
    - "192.168.2.0/24"

  # Initial fencing state
  clusterFence: Unfenced
```

**Storage Configuration on Managed Clusters (Regional DR):**

Ensure StorageClasses have **different** `storageid` labels:

```bash
# On east-cluster
kubectl label storageclass ocs-storagecluster-ceph-rbd \
  ramendr.openshift.io/storageID=ocs-east \
  ramendr.openshift.io/replicationID=rbd-replication

# On west-cluster
kubectl label storageclass ocs-storagecluster-ceph-rbd \
  ramendr.openshift.io/storageID=ocs-west \
  ramendr.openshift.io/replicationID=rbd-replication
```

### Example 2: Metro DR (Sync) DRClusters

For synchronous replication between clusters in the same metro area sharing storage.

**metro-cluster-1 DRCluster:**

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRCluster
metadata:
  name: metro-cluster-1
spec:
  # S3 profile for storing cluster metadata
  s3ProfileName: s3-profile-metro1

  # Network CIDRs required for Metro DR fencing
  cidrs:
    - "172.16.0.0/24"

  # Initial fencing state
  clusterFence: Unfenced
```

**metro-cluster-2 DRCluster:**

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRCluster
metadata:
  name: metro-cluster-2
spec:
  # S3 profile for storing cluster metadata
  s3ProfileName: s3-profile-metro2

  # Network CIDRs required for Metro DR fencing
  cidrs:
    - "172.16.1.0/24"

  # Initial fencing state
  clusterFence: Unfenced
```

**Storage Configuration on Managed Clusters (Metro DR):**

Ensure StorageClasses have the **same** `storageID` label:

```bash
# On metro-cluster-1
kubectl label storageclass ocs-storagecluster-ceph-rbd \
  ramendr.openshift.io/storageID=ocs-shared-metro \
  ramendr.openshift.io/replicationID=shared-replication

# On metro-cluster-2
kubectl label storageclass ocs-storagecluster-ceph-rbd \
  ramendr.openshift.io/storageID=ocs-shared-metro \
  ramendr.openshift.io/replicationID=shared-replication
```

### Creating DRCluster Resources

**On the hub cluster**, create the DRCluster resources:

```bash
# Create east-cluster DRCluster
kubectl apply -f drcluster-east.yaml

# Create west-cluster DRCluster
kubectl apply -f drcluster-west.yaml
```

### Verify DRCluster Resources

#### Check DRCluster Phase

The `phase` field indicates the current state of the DRCluster:

```bash
kubectl get drcluster east-cluster -o jsonpath='{.status.phase}'
```

Expected phases:

- `Starting`: Initial reconciliation in progress
- `Available`: DRCluster is validated and ready

#### Check DRCluster Conditions

Conditions provide detailed validation status:

```bash
kubectl get drcluster east-cluster -o yaml
```

**Look for the `Validated` condition in status:**

```yaml
status:
  phase: Available
  conditions:
    - type: Validated
      status: "True"
      reason: Succeeded
      message: "DRCluster validated successfully"
    - type: Clean
      status: "True"
      reason: Clean
      message: "No fencing resources present"
```

**Possible validation failure reasons:**

- `ConfigMapGetFailed`: Cannot retrieve Ramen ConfigMap
- `s3ConnectionFailed`: Cannot connect to S3 endpoint
- `s3ListFailed`: S3 list operation failed (check credentials and bucket)
- `DrClustersDeployFailed`: Failed to deploy DR components to managed cluster

#### Troubleshooting DRCluster Validation

**If validation fails:**

1. Check S3 connectivity:

   ```bash
   # View detailed conditions
   kubectl describe drcluster east-cluster
   ```

1. Verify S3 profile configuration:

   ```bash
   kubectl get configmap ramen-hub-operator-config -n ramen-system -o yaml
   ```

1. Check S3 secrets exist:

   ```bash
   kubectl get secret s3secret-east-cluster -n ramen-system
   ```

1. Review hub operator logs:

   ```bash
   kubectl logs -n ramen-system deployment/ramen-hub-operator -c manager --tail=100
   ```

1. Verify managed cluster is reachable:

   ```bash
   kubectl get managedcluster east-cluster
   ```

For more troubleshooting information, see
[DRCluster-CRD.md](DRCluster-CRD.md#troubleshooting).

### What Happens After DRCluster Creation

When you create a DRCluster resource, the Ramen hub operator:

1. Validates the S3 profile configuration and tests connectivity
1. Deploys DR components to the managed cluster via ManifestWork (if deployment
   automation is enabled)
1. Creates a DRClusterConfig resource on the managed cluster containing cluster
   identification
1. Monitors cluster health and updates status conditions
1. Prepares for fencing operations by tracking network CIDRs

The DRCluster resource is now ready to be referenced in DRPolicy resources.

## Create a DRPolicy Using the DRClusters

`DRPolicy` resources define the disaster recovery topology and replication configuration
between two managed clusters. A DRPolicy establishes the DR relationship and determines
whether to use synchronous (Metro DR) or asynchronous (Regional DR) replication.

### Understanding DRPolicy

A DRPolicy specifies:

- **Two DRClusters** that participate in DR operations
- **Replication schedule** for async DR (empty for sync DR)
- **Storage class selectors** for replication classes or snapshot classes
- **Automatic storage discovery** populates PeerClass information

### DRPolicy Specification

Key fields:

- `drClusters` (Required, Immutable): List of exactly two cluster names
- `schedulingInterval` (Optional, Immutable): Replication frequency
  - Empty string `""` = Sync (Metro DR)
  - Non-empty (e.g., `5m`, `1h`) = Async (Regional DR)
- `replicationClassSelector` (Optional): Label selector for VolumeReplicationClass
  (async DR)
- `volumeSnapshotClassSelector` (Optional): Label selector for VolumeSnapshotClass (sync
  DR)

### Example 1: Regional DR (Async) Policy

For asynchronous replication between geographically distributed clusters.

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPolicy
metadata:
  name: regional-dr-policy
spec:
  # Two clusters for Regional DR
  drClusters:
    - east-cluster
    - west-cluster

  # Replicate every hour (async replication)
  schedulingInterval: "1h"

  # Select VolumeReplicationClass for async replication
  replicationClassSelector:
    matchLabels:
      ramendr.openshift.io/replication-class: rbd-replication
```

**Prerequisites for async policy:**

1. VolumeReplicationClass configured on both managed clusters
1. Storage replication configured between clusters (e.g., Ceph RBD mirroring)
1. VolumeReplication CRD installed on managed clusters

**Label the VolumeReplicationClass on each managed cluster:**

```bash
# On east-cluster and west-cluster
kubectl label volumereplicationclass <vrc-name> \
  ramendr.openshift.io/replication-class=rbd-replication
```

### Example 2: Metro DR (Sync) Policy

For synchronous replication between clusters in the same metro area.

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPolicy
metadata:
  name: metro-dr-policy
spec:
  # Two clusters for Metro DR
  drClusters:
    - metro-cluster-1
    - metro-cluster-2

  # Empty scheduling interval indicates synchronous replication
  schedulingInterval: ""

  # Select VolumeSnapshotClass for VolSync-based sync replication
  volumeSnapshotClassSelector:
    matchLabels:
      ramendr.openshift.io/snapshot-class: csi-snapclass
```

**Prerequisites for sync policy:**

1. VolumeSnapshotClass configured on both managed clusters
1. VolSync operator installed on managed clusters
1. Shared storage or storage with sync replication between clusters

**Label the VolumeSnapshotClass on each managed cluster:**

```bash
# On metro-cluster-1 and metro-cluster-2
kubectl label volumesnapshotclass <vsc-name> \
  ramendr.openshift.io/snapshot-class=csi-snapclass
```

### Creating a DRPolicy

**On the hub cluster**, create the DRPolicy resource:

```bash
kubectl apply -f drpolicy.yaml
```

### Verify DRPolicy

#### Check DRPolicy Conditions

```bash
kubectl get drpolicy regional-dr-policy -o yaml
```

**Look for the `Validated` condition:**

```yaml
status:
  conditions:
    - type: Validated
      status: "True"
      reason: Succeeded
      message: "DRPolicy validated successfully"
```

#### Check PeerClass Discovery

Ramen automatically discovers storage relationships between the clusters and populates
`PeerClass` information in the DRPolicy status. This indicates which StorageClasses on
the clusters can be used for replication.

**For async (Regional DR) policies:**

```bash
kubectl get drpolicy regional-dr-policy -o jsonpath='{.status.async.peerClasses}' | jq
```

**Example output:**

```json
[
  {
    "storageClassName": "ocs-storagecluster-ceph-rbd",
    "storageID": [
      "ocs-east",
      "ocs-west"
    ],
    "replicationID": "rbd-replication",
    "clusterIDs": [
      "3b6c3e3a-8f2d-4c5b-9a1e-7f8d9c0b1a2e",
      "7d9f4b2c-1a3e-4f5d-8c9b-0a1d2e3f4a5b"
    ]
  }
]
```

**For sync (Metro DR) policies:**

```bash
kubectl get drpolicy metro-dr-policy -o jsonpath='{.status.sync.peerClasses}' | jq
```

**Example output:**

```json
[
  {
    "storageClassName": "ocs-storagecluster-ceph-rbd",
    "storageID": [
      "ocs-shared-metro"
    ],
    "replicationID": "shared-replication",
    "clusterIDs": [
      "5c8d9e0f-2b4a-4d6c-9e1f-8a9b0c1d2e3f",
      "9e1f2a3b-4c5d-6e7f-8a9b-0c1d2e3f4a5b"
    ]
  }
]
```

**Understanding PeerClass output:**

- `storageClassName`: The common StorageClass name available on both clusters
- `storageID`: Storage identifier(s)
  - Single value (same ID) = Sync replication (Metro DR)
  - Multiple values (different IDs) = Async replication (Regional DR)
- `replicationID`: Replication backend identifier
- `clusterIDs`: Kubernetes cluster UIDs (from `kube-system` namespace)

**Important**: Only PVCs using StorageClasses that appear in the `peerClasses` list will
be protected by Ramen. If no PeerClasses are discovered, check your StorageClass labels.

#### Verify StorageClass Labels

If no PeerClasses are discovered, verify StorageClass labels on the managed clusters:

```bash
# On each managed cluster
kubectl get storageclass -o yaml | grep -A 5 "ramendr.openshift.io"
```

**Required labels:**

- `ramendr.openshift.io/storageID`: Must be present and consistent with your DR topology
- `ramendr.openshift.io/replicationID`: Should match across peer clusters

**Add missing labels:**

```bash
kubectl label storageclass <storage-class-name> \
  ramendr.openshift.io/storageID=<storage-id> \
  ramendr.openshift.io/replicationID=<replication-id>
```

#### Complete Verification Checklist

- [ ] DRPolicy `Validated` condition is `True`
- [ ] `peerClasses` is populated in status (under `async` or `sync`)
- [ ] StorageClasses listed in `peerClasses` match your expected storage
- [ ] `storageID` values reflect your DR topology (same for Metro, different for
  Regional)
- [ ] Both DRClusters referenced in the policy show `phase: Available`

### Troubleshooting DRPolicy

**DRPolicy not validated:**

1. Check DRCluster resources exist and are validated:

   ```bash
   kubectl get drcluster
   ```

1. Verify managed clusters are available:

   ```bash
   kubectl get managedclusters
   ```

1. Check for proper StorageClass labels:

   ```bash
   # On each managed cluster
   kubectl get sc -o yaml | grep "ramendr.openshift.io"
   ```

1. Review Ramen hub operator logs:

   ```bash
   kubectl logs -n ramen-system deployment/ramen-hub-operator -c manager | grep -i drpolicy
   ```

**No PeerClasses discovered:**

- Ensure StorageClasses have `ramendr.openshift.io/storageID` labels
- Verify VolumeReplicationClass or VolumeSnapshotClass exists with proper labels
- Check that storage replication is configured between clusters

For additional troubleshooting, see [drpolicy-crd.md](drpolicy-crd.md#troubleshooting).

## Next Steps

After successfully configuring Ramen with DRCluster and DRPolicy resources, you can:

1. Protect your first workload - See [usage.md](usage.md) for detailed instructions on
   creating DRPlacementControl resources
1. Test DR operations - Perform test failovers and relocations
1. Monitor DR status - Set up monitoring and alerting for DR resources
1. Explore advanced features - Learn about Recipe-based protection and custom hooks

### Additional Configuration

Depending on your use case, you may want to configure:

- Velero for Kubernetes object backup and restore
  ([install.md](install.md#velero-for-kube-object-protection))
- Recipe CRD for recipe-based workload protection
- Cluster fencing for Metro DR scenarios
  ([DRCluster-CRD.md](DRCluster-CRD.md#cluster-fencing))
- Maintenance modes for Regional DR ([maintenancemode-crd.md](maintenancemode-crd.md))

### Quick Reference

**Key resources created:**

- **S3 Secrets** (hub cluster, `ramen-system` namespace): Store S3 credentials
- **ConfigMap** (hub cluster, `ramen-system` namespace): Contains S3 profile
  configuration
- **DRCluster** (hub cluster, cluster-scoped): One per managed cluster
- **DRPolicy** (hub cluster, cluster-scoped): One per cluster pair

**Verification commands:**

```bash
# Check DRClusters
kubectl get drcluster

# Check DRPolicies
kubectl get drpolicy

# Check PeerClasses
kubectl get drpolicy <policy-name> -o jsonpath='{.status.async.peerClasses}' | jq
kubectl get drpolicy <policy-name> -o jsonpath='{.status.sync.peerClasses}' | jq

# Check DRCluster validation
kubectl get drcluster <cluster-name> -o jsonpath='{.status.conditions[?(@.type=="Validated")]}' | jq
```

For more detailed information about each resource, refer to:

- [DRCluster-CRD.md](DRCluster-CRD.md) - Complete DRCluster reference
- [drpolicy-crd.md](drpolicy-crd.md) - Complete DRPolicy reference
- [install.md](install.md) - Installation guide
- [usage.md](usage.md) - Protecting and managing workloads
