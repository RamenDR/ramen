# Install

## **Under construction**

## Prerequisites

### OCM based multi-cluster setup

Ramen works as part of the [OCM hub] to orchestrate disaster-recovery, of
application state, for applications deployed to [OCM managed clusters].

As a result Ramen requires that multiple kubernetes clusters, that form the
set of peer clusters, across which application deployment and state needs to
be managed for disaster recovery are, managed clusters of an OCM hub.

The OCM hub orchestration is provided by Ramen using the
[DRPlacementControl(DRPC) CRD](docs/drpc-crd.md).

### Ceph-CSI as the volume provisioner

Ramen also works as part of the [OCM managed clusters] to orchestrate,

- [VolumeReplication](https://github.com/csi-addons/volume-replication-operator/blob/main/api/v1alpha1/volumereplication_types.go)
  resources for all PVCs of an application
- Preserving metadata regarding each PVC that is replicated

VolumeReplication resources require storage providers to support
[CSI extensions](https://github.com/csi-addons/spec) that enable managing
volume replication features for volumes provisioned by the storage provider.
[ceph-CSI](https://github.com/ceph/ceph-csi/) is one such storage provider
that supports the required extensions.

The managed cluster orchestration is provided by Ramen using the
[VolumeReplicationGroup(VRG) CRD](docs/vrg-crd.md).

**NOTE:** Ramen on OCM managed clusters and its orchestration of VRG CRD can
be accomplished independent of an OCM setup, and only requires a native
kubernetes cluster to manage VRG resources.

### S3 store

Ramen preserves metadata related to VolumeReplication resources in an S3
compatible object store. An S3 store endpoint is hence required as part of the
setup.

Ramen specifically stores PV metadata for a replicated PVC, to restore the same
across peer cluster prior to deploying the PVCs of the application, to ensure
proper binding of the PVC resources to the replicated storage end points.

## Installing Ramen on OCM hub

## Installing Ramen on managed kubernetes clusters
