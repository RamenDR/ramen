# Install

## Prerequisites

### OCM managed multi-cluster setup

Ramen works as part of the [OCM hub](https://open-cluster-management.io/concepts/architecture/#hub-cluster)
cluster to orchestrate the [placement](https://open-cluster-management.io/concepts/placement/)
of [workloads](https://kubernetes.io/docs/concepts/workloads/) and their attachment
to PersistentVolumes, on [OCM managed clusters](https://open-cluster-management.io/concepts/managedcluster/).

[Ramen hub](#ramen-hub) and [Ramen cluster](#ramen-cluster) components hence
require an OCM managed multi-cluster setup for their operation.

### OCM Managed Cluster supporting VolumeReplication CRD

Ramen also works as part of the [OCM managed clusters](https://open-cluster-management.io/concepts/managedcluster/)
to orchestrate,

- [VolumeReplication](https://github.com/csi-addons/volume-replication-operator/blob/main/api/v1alpha1/volumereplication_types.go)
  resources for all PVCs of a workload
- Preserving relevant cluster data regarding each PVC that is replicated

VolumeReplication resources require storage providers to support
[CSI extensions](https://github.com/csi-addons/spec) that enable managing
volume replication features for volumes provisioned by the storage provider.
[Ceph-CSI](https://github.com/ceph/ceph-csi/) is one such storage provider
that supports the required extensions.

[Ramen cluster](#ramen-cluster) component hence should be deployed to OCM
managed clusters that support VolumeReplication extensions.

### S3 store

Ramen preserves cluster data related to PVC resources in an S3 compatible
object store. An S3 store endpoint is hence required as part of the setup.

**NOTE**: Ramen specifically stores PV cluster data for a replicated PVC, to
restore the same across peer cluster prior to deploying the PVCs of the
workload, this ensures proper binding of the PVC resources to the replicated
storage end points.

### Operator lifecycle manager (OLM)

Ramen components are provided as [OLM](https://olm.operatorframework.io/docs/getting-started/)
catalog sources in the [Ramen catalog](https://quay.io/repository/ramendr/ramen-operator-catalog?tab=info).

All clusters that require Ramen hub or cluster components installed, require
OLM installed on the same.

### Kubernetes versions

Kubernetes versions supported are [1.20](https://kubernetes.io/releases/)
or higher.

### Tool versions

Installation and deployment require the following tools at specified versions
(or higher):

- kubectl > v1.21
    - kubectl version can be verfied using

      ```bash
      kubectl version
      ```

## Ramen hub

`ramen-hub-operator` is the controller for managing the life cycle of user
created [DRPlacementControl (DRPC)](drpc-crd.md) Ramen API resources and
administrator created [DRPolicy](drpolicy-crd.md) Ramen API resources, and is
installed on the **OCM hub cluster**.

### Install ramen-hub-operator

To install `ramen-hub-operator` configure [kubectl](https://kubernetes.io/docs/concepts/configuration/organize-cluster-access-kubeconfig/#context)
to use the desired OCM hub cluster and execute:

```bash
kubectl apply -k github.com/RamenDR/ramen/config/olm-install/hub/?ref=main
```

**NOTE**: By default `ramen-hub-operator` creates a deployment for its
controller in the `ramen-system` namespace. To verify check the health of the
deployment:

```bash
kubectl get deployments -n ramen-system ramen-hub-operator
```

## Ramen cluster

`ramen-dr-cluster-operator` is the controller for managing the life cycle of
[VolumeReplicationGroup](vrg-crd.md) Ramen API resources and is installed on
the **OCM managed clusters**.

### Install ramen-dr-cluster-operator

To install `ramen-dr-cluster-operator` configure [kubectl](https://kubernetes.io/docs/concepts/configuration/organize-cluster-access-kubeconfig/#context)
to use the desired OCM managed cluster and execute:

```bash
kubectl apply -k github.com/RamenDR/ramen/config/olm-install/dr-cluster/?ref=main
```

**NOTE**: By default `ramen-dr-cluster-operator` creates a deployment for its
controller in the `ramen-system` namespace. To verify check the health of
the deployment:

```bash
kubectl get deployments -n ramen-system ramen-dr-cluster-operator
```
