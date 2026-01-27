<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Installing Ramen

This guide describes how to install Ramen's disaster recovery operators on your
cluster environment.

## Overview

Ramen consists of two operators that work together to provide disaster recovery
capabilities:

1. **Ramen Hub Operator** - Installed on the OCM hub cluster, orchestrates DR
   operations across managed clusters
1. **Ramen DR Cluster Operator** - Installed on each OCM managed cluster,
   manages local volume replication and workload protection

## Prerequisites

Before installing Ramen, ensure the following requirements are met:

### 1. Open Cluster Management (OCM) Setup

Ramen requires an [OCM hub cluster](https://open-cluster-management.io/concepts/architecture/#hub-cluster)
with at least two [managed clusters](https://open-cluster-management.io/concepts/managedcluster/)
for disaster recovery operations.

**Requirements:**

- OCM hub cluster with `multicluster-engine` or
  `advanced-cluster-management` installed
- At least 2 managed clusters registered with the hub
- Clusters must be able to communicate for replication (directly or via
  Submariner)

**Verify OCM setup:**

```bash
# On the hub cluster
kubectl get managedclusters
```

You should see at least two managed clusters in `Ready` state.

### 2. Storage Replication Support

Each managed cluster must have storage that supports volume replication through
one of these methods:

#### Option A: Async Replication (VolumeReplication)

Storage must support the CSI
[VolumeReplication](https://github.com/csi-addons/volume-replication-operator)
extensions.

**Supported storage providers:**

- [Ceph-CSI](https://github.com/ceph/ceph-csi/) with RBD mirroring
- Other CSI drivers implementing the VolumeReplication spec

**Requirements:**

- VolumeReplication CRD installed on managed clusters
- VolumeReplicationClass configured for your storage
- Storage replication configured between peer clusters (e.g., Ceph RBD
  mirroring)

**Verify VolumeReplication support:**

```bash
# On each managed cluster
kubectl get crd volumereplicationclasses.replication.storage.openshift.io
kubectl get volumereplicationclass
```

#### Option B: Sync Replication (VolSync)

Storage must support CSI snapshots for VolSync-based replication.

**Requirements:**

- VolSync operator installed on managed clusters
- VolumeSnapshotClass configured
- CSI driver supporting snapshots

**Verify VolSync support:**

```bash
# On each managed cluster
kubectl get csv -n openshift-operators | grep volsync
kubectl get volumesnapshotclass
```

### 3. S3 Object Storage

Ramen stores cluster metadata (PV specs, VRG state) in S3-compatible object
storage for cross-cluster recovery.

**Requirements:**

- S3-compatible object store accessible from all managed clusters
- Bucket(s) created for each managed cluster
- S3 credentials (access key and secret key)

**Supported S3 providers:**

- AWS S3
- Ceph RGW (RADOS Gateway)
- MinIO
- Any other S3-compatible storage

**S3 access will be configured after installation** - see
[configure.md](configure.md).

### 4. Operator Lifecycle Manager (OLM)

Ramen operators are distributed via OLM catalogs.

**Requirements:**

- OLM installed on hub and managed clusters
- OpenShift 4.x includes OLM by default
- For vanilla Kubernetes, [install
  OLM](https://olm.operatorframework.io/docs/getting-started/)

**Verify OLM:**

```bash
kubectl get csv -n openshift-operators  # or other namespace
```

### 5. Kubernetes/OpenShift Versions

**Supported versions:**

- Kubernetes 1.20 or higher
- OpenShift 4.10 or higher (recommended for full feature support)

**Verify cluster version:**

```bash
kubectl version --short
```

### 6. Required Tools

The following tools must be installed on your workstation:

- **kubectl** >= v1.21 - Kubernetes CLI

  ```bash
  kubectl version --client
  ```

- **oc** (optional) - OpenShift CLI, if using OpenShift

  ```bash
  oc version --client
  ```

### 7. Optional Components

These components enhance Ramen's capabilities:

#### Velero (for Kube Object Protection)

Required if protecting applications using discovered application or Recipe
methods.

**Install on each managed cluster:**

```bash
# Example using Helm
helm repo add vmware-tanzu https://vmware-tanzu.github.io/helm-charts
helm install velero vmware-tanzu/velero --namespace velero --create-namespace \
  --set configuration.backupStorageLocation[0].bucket=<your-bucket> \
  --set configuration.backupStorageLocation[0].config.region=<region> \
  --set-file credentials.secretContents.cloud=<path-to-credentials>
```

**Verify:**

```bash
kubectl get deploy -n velero
```

#### Recipe CRD (for Recipe-based Protection)

Required if using Recipe-based workload protection.

The Recipe CRD is available at:
[recipe/config/crd/bases/ramendr.openshift.io_recipes.yaml](https://github.com/RamenDR/recipe/blob/main/config/crd/bases/ramendr.openshift.io_recipes.yaml)

Installation instructions are in [recipe.md](recipe.md).

## Installation Steps

### Step 1: Install Ramen Hub Operator

The `ramen-hub-operator` manages disaster recovery orchestration on the OCM hub
cluster. It controls:

- [DRPlacementControl (DRPC)](drpc-crd.md) - DR operations for individual
  applications
- [DRPolicy](drpolicy-crd.md) - DR topology and replication configuration
- DRCluster - Managed cluster registration and S3 configuration

#### Install on Hub Cluster

Configure kubectl to use your OCM hub cluster context:

```bash
kubectl config use-context <hub-cluster-context>
```

Install the operator using OLM:

```bash
kubectl apply -k github.com/RamenDR/ramen/config/olm-install/hub/?ref=main
```

This creates:

- `ramen-system` namespace
- Ramen hub operator deployment
- Required CRDs (DRPlacementControl, DRPolicy, DRCluster)
- RBAC resources

#### Verify Hub Operator Installation

1. **Check operator deployment:**

   ```bash
   kubectl get deployments -n ramen-system
   ```

   Expected output:

   ```
   NAME                 READY   UP-TO-DATE   AVAILABLE   AGE
   ramen-hub-operator   1/1     1            1           2m
   ```

1. **Check operator pod status:**

   ```bash
   kubectl get pods -n ramen-system
   ```

   Expected output:

   ```
   NAME                                  READY   STATUS    RESTARTS   AGE
   ramen-hub-operator-xxxxxxxxxx-xxxxx   2/2     Running   0          2m
   ```

1. **Check CRDs are installed:**

   ```bash
   kubectl get crd | grep ramendr
   ```

   Expected CRDs:

   ```
   drplacementcontrols.ramendr.openshift.io
   drpolicies.ramendr.openshift.io
   drclusters.ramendr.openshift.io
   ```

1. **Check operator logs for errors:**

   ```bash
   kubectl logs -n ramen-system deployment/ramen-hub-operator -c manager
   ```

### Step 2: Install Ramen DR Cluster Operator

The `ramen-dr-cluster-operator` manages volume replication and workload
protection on each managed cluster. It controls:

- [VolumeReplicationGroup (VRG)](vrg-crd.md) - PVC replication and application
  protection
- Volume replication coordination (VolumeReplication or VolSync)
- Kube object capture and recovery

**Note:** The hub operator creates VRG resources on managed clusters via
ManifestWork - you don't create them manually.

#### Install on Each Managed Cluster

Repeat these steps for each managed cluster that will participate in DR.

Configure kubectl to use the managed cluster context:

```bash
kubectl config use-context <managed-cluster-context>
```

Install the operator using OLM:

```bash
kubectl apply -k github.com/RamenDR/ramen/config/olm-install/dr-cluster/?ref=main
```

This creates:

- `ramen-system` namespace
- Ramen DR cluster operator deployment
- Required CRDs (VolumeReplicationGroup, DRClusterConfig, etc.)
- RBAC resources

#### Verify DR Cluster Operator Installation

1. **Check operator deployment:**

   ```bash
   kubectl get deployments -n ramen-system
   ```

   Expected output:

   ```
   NAME                        READY   UP-TO-DATE   AVAILABLE   AGE
   ramen-dr-cluster-operator   1/1     1            1           2m
   ```

1. **Check operator pod status:**

   ```bash
   kubectl get pods -n ramen-system
   ```

   Expected output:

   ```
   NAME                                         READY   STATUS    RESTARTS   AGE
   ramen-dr-cluster-operator-xxxxxxxxxx-xxxxx   2/2     Running   0          2m
   ```

1. **Check CRDs are installed:**

   ```bash
   kubectl get crd | grep ramendr
   ```

   Expected CRDs:

   ```
   volumereplicationgroups.ramendr.openshift.io
   drclusterconfigs.ramendr.openshift.io
   maintenancemodes.ramendr.openshift.io
   ```

1. **Check operator logs:**

   ```bash
   kubectl logs -n ramen-system deployment/ramen-dr-cluster-operator -c manager
   ```

### Step 3: Verify Multi-Cluster Installation

Switch back to the hub cluster and verify operators are running on all clusters:

```bash
kubectl config use-context <hub-cluster-context>

# Check hub operator
kubectl get deploy -n ramen-system ramen-hub-operator

# Check managed cluster operators via kubectl with context
for cluster in dr1 dr2; do
  echo "Checking cluster: $cluster"
  kubectl get deploy -n ramen-system ramen-dr-cluster-operator --context $cluster
done
```

## Post-Installation

After installing Ramen operators, you need to configure them for your environment.

### Next Steps

1. **Configure Ramen** - Set up DRPolicy, DRCluster resources, and S3 storage

    - See [configure.md](configure.md) for detailed configuration instructions

1. **Verify storage replication** - Ensure volume replication is working

    - Test VolumeReplication or VolSync on your managed clusters

1. **Protect your first workload** - Deploy an application with DR protection

    - See [usage.md](usage.md) for workload protection methods

### Configuration Prerequisites

Before configuring Ramen, prepare the following information:

- **S3 credentials** for each managed cluster

    - Bucket names
    - Access key and secret key
    - S3 endpoint URL

- **Cluster information**

    - Managed cluster names (as registered in OCM)
    - Storage class names for PVCs
    - VolumeReplicationClass or VolumeSnapshotClass names

- **Replication schedule** (for async DR)

    - How often to replicate (e.g., "5m", "1h")

## Troubleshooting Installation

### Operator Pod Not Running

**Check pod status:**

```bash
kubectl describe pod -n ramen-system <pod-name>
```

**Common issues:**

- Image pull errors - check image registry access
- OLM not installed - verify `kubectl get csv -n openshift-operators`
- Resource constraints - check cluster resources

### CRDs Not Created

**Verify OLM created the subscription:**

```bash
kubectl get subscription -n ramen-system
kubectl get installplan -n ramen-system
```

**Check catalog source:**

```bash
kubectl get catalogsource -n openshift-marketplace
```

### Operator Logs Show Errors

**View logs:**

```bash
kubectl logs -n ramen-system deployment/ramen-hub-operator -c manager --tail=100
```

**Common errors:**

- RBAC permissions - check ClusterRole and ClusterRoleBinding
- API server connection issues - verify network connectivity
- Missing dependencies - ensure OCM is properly installed

### Manual Cleanup (if needed)

If you need to uninstall and reinstall:

**Remove hub operator:**

```bash
kubectl delete -k github.com/RamenDR/ramen/config/olm-install/hub/?ref=main
```

**Remove DR cluster operator:**

```bash
kubectl delete -k github.com/RamenDR/ramen/config/olm-install/dr-cluster/?ref=main
```

**Note:** This will remove the operators but not the CRDs. To remove CRDs:

```bash
kubectl delete crd drplacementcontrols.ramendr.openshift.io
kubectl delete crd drpolicies.ramendr.openshift.io
kubectl delete crd drclusters.ramendr.openshift.io
kubectl delete crd volumereplicationgroups.ramendr.openshift.io
```

## Development Installation

For development and testing, see:

- [user-quick-start.md](user-quick-start.md) - Complete test environment setup
  with drenv
- [devel-quick-start.md](devel-quick-start.md) - Developer setup for
  contributing to Ramen
