<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# PVC replication protection offload for Async DR

## Motivation

Ramen leverages orchestrating volume replication for storage systems that have
implemented the csi-addons support for VolumeReplication, or VolumeGroupReplication,
APIs as detailed in the CSI [storage replication addon](https://github.com/csi-addons/kubernetes-csi-addons/tree/main/api/replication.storage/v1alpha1)
specification.

For storage systems that do not implement the csi-addons specification, Ramen can
leverage VolumeSnapshots or VolumeGroupSnapshots CSI specification for periodic
snapshot based replication of volumes using volsync and the underlying rsync
plugin in volsync.

For storage systems that have other APIs to manage volume replication, or have
external components that control and manage volume replication, Ramen currently
does not have an API, or mechanism, to offload replication management.

To aid storage systems that fall into the above category, the ability to
offload volume replication management is required.

This would enable users to leverage Ramen integrations, like k8s resource
protection, or ArgoCD based application failover or relocation management,
while leveraging existing storage system replication management for data.

## Design

To offload volume (PVC) replication to external systems Ramen would,

- Leverage csi-addon VolumeGroupReplication [`spec.external`](https://github.com/csi-addons/kubernetes-csi-addons/blob/main/docs/volumegroupreplication.md)
  fields capability to prevent the csi-addons controllers from reconciling a
  VolumeGroupReplication resource.
- Detect that a StorageClass across the 2 OCM ManagedClusters, that is named the
  same and is backed by the same CSI driver, has opted into offload management of
  PVCs that are provisioned using the StorageClass, by checking for the presence
  of the label `ramendr.openshift.io/offloaded` on the StorageClass in both clusters.
- Further the StorageClass should be linked to a VolumeGroupReplicationClass on
  the ManagedCluster (via the `ramendr.openshift.io/storageid` label carrying
  the same storageid value), and the value for `ramendr.openshift.io/replicationid`
  label on the VolumeGroupReplicationClass should match across the ManagedClusters
- This information is automatically stored in the appropriate peerClass status
  entry in a DRPolicy created at the OCM hub that includes the 2 ManagedClusters.
- Workloads that are DR protected at the OCM hub using such a DRPolicy, would be
  passed the auto detected peerClass values as a spec parameter to the
  VolumeReplicationGroup resource created on the ManagedCluster for workload
  DR protection orchestration.
- PVCs of the workload that are to be DR protected, based on the VRG PVC label
  selector, are inspected to see if they are provisioned from StorageClasses
  that have opted in for offload management, and if so, Ramen would create a
  VolumeGroupReplication(VGR) resource in the respective namespaces that belong
  to the workload, with the additional `spec.external` field set to true, that
  instructs the CSI storage replication addon controllers to ignore
  reconciliation of this resource.
- As the VGR is marked offloaded, its status needs to be updated by a user or
  other orchestrators that maybe used to reconcile a VGR for a specific
  storage system, once its desired state is achieved.
- Further Ramen orchestration will continue to process VGR status as earlier,
  to determine when a group of PVCs in a namespace transition to various replication
  states as defined by the VGR API.

### Limitations

- A workload that contains PVCs that belong to both offloaded and non-offloaded
  storage backends would not be supported.
- The VGR status contains timestamps regarding when the group of PVCs were last
  replicated to the peer storage system. These are used to provide metrics and
  raise alerts on the OCM hub cluster. If the VGR status is not automated to be
  periodically updated, alerts may fire on the OCM cluster that would need to be
 ignored.
- Ramen preserves PVs and PVCs of the workload, and recreates them on a peer
  cluster, thus providing VGR a reference to a list of PVCs that need to be in a
  desired replication state. Storage systems would hence need to be able to use
  the same PV definition across peered/replicated storage backends to access the
  replicated volume.
- Ramen as part of cleanup of the workload on a cluster that is currently
  Secondary in the replication relationship, deletes PVs (and PVCs) that are part
  of the workload. The assumption by Ramen is that the PVs are referencing
  secondary instances in the storage backend, and hence can be garbage collected.
  For storage systems that require that such PVs not be deleted, would need to
  leverage a StorageClass that prefers the "Retain" strategy as its ReclaimPolicy.
  This also leads to users manually deleting the PV on the Secondary cluster, once
  the workload has been DR disabled.
- 2-way replication is assumed

### Future considerations

- Metrics maybe turned off
- Some watches in Ramen maybe made optional

## Configuration

**NOTE:** The steps below can be done before or after creation of a DRPolicy
including the ManagedClusters on the OCM hub cluster

1. Label StorageClass as supporting Ramen based DR protection on OCM ManagedClusters
   that are part of a DRPolicy
    1. The StorageClass should be named the same across both ManagedClusters
    1. The StorageClass should be backed by the same CSI driver across both
       ManagedClusters
    1. Label the StorageClass with the label "ramendr.openshift.io/storageid"
       and a value that is unique across these and other ManagedClusters.
    1. Label to denote offload support by the SC "ramendr.openshift.io/offloaded"
       with any value

```yaml
# Example StorageClass on both clusters
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: rook-ceph-block
  labels:
    ramendr.openshift.io/storageid: "ceph-cluster-a-pool-b-ns-c"
    ramendr.openshift.io/offloaded: "true" # Key label for offload
provisioner: rook-ceph.rbd.csi.ceph.com
...
```

1. Configure VGRClass
    1. Install related VGR and VGRClass CRDs
    1. Create VGRClass with schedule matching the DRPolicy
    1. Label VGRClass with storageID to link to SC on the ManagedCluster to the SC
    1. Label VGRClass with replicationID to link the VGRClass across clusters
        1. The replicationID value is unique to signify an established replication
           relationship across the two ManagedClusters

```yaml
# Example VolumeGroupReplicationClass on both clusters
apiVersion: replication.storage.csi-addons.io/v1alpha1
kind: VolumeGroupReplicationClass
metadata:
  name: rook-ceph-block-vgrclass
  labels:
    # Links to StorageClass ramendr.openshift.io/storageid label value
    ramendr.openshift.io/storageid: "ceph-cluster-a-pool-b-ns-c"
    # Unique replication relationship identifier across the 2 ManagedClusters
    ramendr.openshift.io/replicationid: "ceph-cluster-a-pool-b-ns-c-east-west-repl"
spec:
  provisioner: rook-ceph.rbd.csi.ceph.com
  parameters:
    # Schedule should match DRPolicy schedule
    schedulingInterval: 1m
    ...
```

**NOTE:** The setup for replication of data across the 2 clusters backed by the
specific storage driver and instance is done outside the Ramen or DR flow.

The DRPolicy on the OCM hub cluster can be validated to ensure its status reflects
the above StorageClass as an offloaded peer.

```yaml
# Example DRPolicy status
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPolicy
metadata:
name: dr-policy
spec:
    drClusters:
    - east
    - west
    schedulingInterval: 1m
status:
    async:
        peerClasses:
        - clusterIDs:
          - c7809412-37b9-409a-943a-aa5a5d5b59d3
          - e319ed9b-1870-4c31-8fd1-cf5eea08f5e6
          storageClassName: rook-ceph-block
          storageID:
          - ceph-cluster-a-pool-b-ns-c
          - ceph-cluster-b-pool-b-ns-c
          grouping: true
          offloaded: true # Offloaded detected
          replicationID: ceph-cluster-a-pool-b-ns-c-east-west-repl
```

## DR workflow

A workload that is either managed by ArgoCD on the OCM hub cluster or is deployed
on the ManagedCluster (classified as discovered workloads) can be DR protected.

### Enable DR

- Create a DRPlacementControl (DRPC) for the workload
- This would result in one or more VGR resources across namespaces that the DRPC
  protects, on the ManagedCluster where the workload is deployed
    - VGR spec.state would be Primary
    - The VGR spec.external is set to true, indicating that an external entity
      needs to operate and update the status of the VGR
- Inspect VGRs created and use vendor specific instructions (or operators) to
  enable PVCs that belong to the VGR for replication
    - TODO: How to know which namespaces and name to inspect VGRs in?
    - NOTE: Adding a vendor specific finalizer to the VGR would be useful to
      deal with required actions to take at the storage layer when the resource
      is deleted for other DR actions

- Update VGR status to denote replication status and health
- Inspect DRPC at the OCM hub cluster, specifically the Protected condition, as
  this should report True once the VGR status is updated

```yaml
# Sample status update to VGR
  status:
    persistentVolumeClaimsRefList:
    - name: busybox-pvc
    conditions:
    - lastTransitionTime: "2025-07-30T18:04:00Z"
      message: volume is promoted to primary and replicating to secondary
      observedGeneration: 1
      reason: Promoted
      status: "True"
      type: Completed
    - lastTransitionTime: "2025-07-30T18:04:00Z"
      message: volume is healthy
      observedGeneration: 1
      reason: Healthy
      status: "False"
      type: Degraded
    - lastTransitionTime: "2025-07-30T18:04:00Z"
      message: volume is not resyncing
      observedGeneration: 1
      reason: NotResyncing
      status: "False"
      type: Resyncing
    lastCompletionTime: "2025-07-30T18:05:00Z"
    lastSyncBytes: 1708032
    lastSyncDuration: 0s
    lastSyncTime: "2025-07-30T18:04:00Z"
    message: volume is marked primary
    observedGeneration: 1
    state: Primary
```

### Failover

- Update DRPC on the OCM hub cluster to change its action to Failover, and update
  failoverCluster to indicate the cluster to failover to
- This would result in one or more VGR resources across namespaces that the DRPC
  protects, on the ManagedCluster where the workload needs to failover to
    - The VGR spec.external is set to true, indicating that an external entity
      needs to operate and update the status of the VGR
    - VGR spec.state would be Primary
- Inspect VGRs created and use vendor specific instructions (or operators) to
  promote PVCs that belong to the VGR for replication
- Update VGR status to denote replication status and health
- Inspect DRPC at the OCM hub cluster, specifically the `status.progression`, as
  this should report `Cleaning Up` once the VGR status is updated
- Inspect the ManagedCluster where the workload was failed over to, the application
  pods and resources should have been deployed to the cluster

#### Cleanup of older cluster post Failover

Post recovery of the failed cluster, which led to the Failover action, the following
workflow needs to be performed on the failed cluster to reset the replication for
the volumes

- Ramen orchestrators would update the VGR resources to request that the PVCs be
  moved to a Secondary role to sync its contents with the current Primary volume
  from the failover cluster
    - VGR spec.state will be Secondary
    - VGR spec.resync will be true
- Inspect VGRs created and use vendor specific instructions (or operators) to demote
  the PVCs that belong to the VGR for replication to Secondary roles
- Update VGR status to denote replication status and health
- Inspect DRPC at the OCM hub cluster, specifically the PeerReady condition, as
  this should report True once the VGR status is updated
- Inspect the ManagedCluster where the workload was cleaned up, the PVCs and workload
  pods/resources should have been deleted
    - **NOTE:** For discovered workloads, the cleanup of the PVCs or workload resources
      is a user responsibility

### Relocate

- Update DRPC on the OCM hub cluster to change its action to Relocate, and update
  preferredCluster to indicate the cluster to relocate to
- Inspect VGRs already created on the currently deployed cluster and use vendor
  specific instructions (or operators) to demote the PVCs that belong to the VGR
  for replication to Secondary roles
    - VGR spec.state will be Secondary
- Update VGR status to denote replication status and health
- Relocation will now proceed to create one or more VGR resources on the
  preferred cluster
- Inspect VGRs created and use vendor specific instructions (or operators) to promote
  PVCs that belong to the VGR for replication
- Update VGR status on the preferred cluster to denote replication status and health
- Inspect DRPC at the OCM hub cluster, specifically the Available condition, as
  this should report True once the VGR status is updated
- Inspect the ManagedCluster where the workload was relocated to, the application
  pods and resources should have been deployed to the cluster

### Disable DR

- Delete DRPC resource at the OCM hub that is DR protecting the workload
- Inspect VGRs already created on the currently deployed cluster and use vendor
  specific instructions (or operators) to disable replication for the related PVCs
    - VGRs will be deleted, a useful aid is to add a vendor specific finalizer to
      the VGR to process deletion related replication workflow
