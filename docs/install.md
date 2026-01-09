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

### 1. Kubernetes Versions

**Supported versions:**

- Kubernetes 1.30 or higher

### 2. Open Cluster Management (OCM) Setup

Ramen requires an
[OCM](https://open-cluster-management.io/docs/concepts/architecture/) hub cluster
with at least two managed clusters for disaster recovery operations.

**Requirements:**

- OCM hub cluster with `multicluster-engine`, `ocm-controller` (from
  multicloud-operators-foundation), and hub addons
  (`application-manager`, `governance-policy-framework`)
- OCM managed cluster addons on each managed cluster:
  `application-manager`, `governance-policy-framework`, and
  `config-policy-controller`
- At least 2 managed clusters registered with the hub
- All clusters must be able to communicate with each other

For OCM installation instructions, see the [OCM installation guide](https://open-cluster-management.io/docs/getting-started/installation/).

### 3. Storage Replication Support

Ramen supports two disaster recovery modes, each with different storage requirements:

#### Sync Mode (Metro DR)

Sync mode uses an external storage cluster that all managed clusters connect
to, providing all clusters access to the same storage backend.

**Supported:**

- Any CSI-compatible storage systems that support shared external storage
    clusters
- CSI drivers that support static provisioning for PVCs and can attach the
    same storage when a PV is transferred between clusters sharing the same
    storage backend

**Required:**

- External storage cluster
- Storage provider installed that supports CSI and synchronous replication
- StorageClasses on managed clusters with the same
    `ramendr.openshift.io/storageid` labels (indicating shared storage)
- Low-latency network connectivity between managed clusters and the external
    storage cluster

#### Async Mode (Regional DR)

Async mode uses storage in each managed cluster with asynchronous replication
between clusters based on configurable time intervals.

**Supported:**

- Any CSI-compatible storage that supports VolumeReplication or
    VolumeSnapshot

**Required:**

- Storage provider installed in each managed cluster that supports CSI and
    VolumeReplication or VolumeSnapshot
- StorageClasses on managed clusters with different
    `ramendr.openshift.io/storageid` labels (indicating separate storage
    instances)
- [VolumeReplication](https://github.com/csi-addons/volume-replication-operator)
    CRD and VolumeReplicationClass OR VolSync operator and VolumeSnapshotClass
- Network connectivity between managed clusters for replication

For information about installing storage providers that support CSI, see your
storage vendor's documentation or the
[Kubernetes CSI documentation](https://kubernetes-csi.github.io/docs/).

### 4. S3 Object Storage

Ramen stores workload metadata and Ramen resources in S3-compatible object
storage for cross-cluster recovery.

**Requirements:**

- S3-compatible object store accessible from all managed clusters
- Bucket(s) created for each managed cluster
- S3 credentials (access key and secret key)

**Supported:**

- Any S3-compatible storage

### 5. Operator Lifecycle Manager (OLM)

Ramen operators are distributed via OLM catalogs.

**Requirements:**

- OLM installed on hub and managed clusters
- For vanilla Kubernetes, [install OLM](https://olm.operatorframework.io/docs/getting-started/)

### 6. Required Tools

The following tools must be installed on your workstation:

- **kubectl** >= v1.30 - Kubernetes CLI

### 7. Optional Components

These components enhance Ramen's capabilities:

#### Recipe CRD (for Recipe-based Protection)

Required if using Recipe-based workload protection.

**Install on each managed cluster:**

```bash
kubectl apply -k "https://github.com/RamenDR/recipe.git/config/crd?ref=main"
```

The Recipe CRD is also available at:
[recipe/config/crd/bases/ramendr.openshift.io_recipes.yaml](https://github.com/RamenDR/recipe/blob/main/config/crd/bases/ramendr.openshift.io_recipes.yaml)

## Installation Steps

### Step 1: Install Ramen Hub Operator

The `ramen-hub-operator` manages disaster recovery orchestration on the OCM hub
cluster. It controls:

- [DRPlacementControl (DRPC)](drpc-crd.md) - DR operations for individual
  applications
- [DRPolicy](drpolicy-crd.md) - DR topology and replication configuration
- [DRCluster](drcluster-crd.md) - Managed cluster registration and S3 configuration

#### Install on Hub Cluster

Configure kubectl to use your OCM hub cluster context:

```bash
kubectl config use-context <hub-cluster-context>
```

Install the operator using OLM:

```bash
kubectl apply -k "https://github.com/RamenDR/ramen/config/olm-install/hub?ref=main"
```

This creates:

- `ramen-system` namespace
- Ramen hub operator deployment
- Required CRDs (DRPlacementControl, DRPolicy, DRCluster)
- RBAC resources

#### Verify Hub Operator Installation

**Check operator deployment:**

```bash
kubectl get deployments -n ramen-system
```

Expected output:

```
NAME                 READY   UP-TO-DATE   AVAILABLE   AGE
ramen-hub-operator   1/1     1            1           2m
```

### Step 2: Install Ramen DR Cluster Operator

Install the catalog source for the DR Cluster operator in all managed clusters.

Switch to each managed cluster context:

```bash
kubectl config use-context <managed-cluster-context>
```

Install the catalog source:

```bash
kubectl apply -k "https://github.com/RamenDR/ramen/config/olm-install/base?ref=main"
```

The DR Cluster operator will be installed automatically during configuration.
Refer to [configure.md](configure.md) for more details.

## Post-Installation

After installing Ramen operators, you need to configure them for your environment.

### Next Steps

**Configure Ramen** - Set up DRPolicy, DRCluster resources, and S3 storage.
See [configure.md](configure.md) for detailed configuration instructions.

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

### Rollback Installation

**Note:** If Ramen has been configured, roll back configurations before rolling
back the installation.

**Remove hub operator:**

```bash
kubectl delete -k "https://github.com/RamenDR/ramen/config/olm-install/hub?ref=main"
```

**Note:** This will remove the operators but not the CRDs. To remove CRDs:

```bash
kubectl delete crd drplacementcontrols.ramendr.openshift.io
kubectl delete crd drpolicies.ramendr.openshift.io
kubectl delete crd drclusters.ramendr.openshift.io
```

**Remove catalog source in all managed clusters:**

```bash
kubectl delete -k "https://github.com/RamenDR/ramen/config/olm-install/base?ref=main"
```

## Development Installation

For development and testing, see:

- [user-quick-start.md](user-quick-start.md) - Complete test environment setup
  with drenv
- [devel-quick-start.md](devel-quick-start.md) - Developer setup for
  contributing to Ramen
