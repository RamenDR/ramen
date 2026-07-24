# Snap/Replication Class Determination Across Clusters

## Problem definition

In the future we WILL have multiple storage instances connected to a single
ManagedCluster and a DRPolicy across such ManagedClusters created. For example
ManagedCluster with multiple Rook and external Ceph clusters connected.

This would result in multiple StorageClass(SC), VolumeSnapshotClass (VSC), and
VolumeReplicationClass (VRC). For reasons such as, different pools, different
secrets for each class.

Given the above we need to solve the following problems for Ramen

- Determining which PVCs can be reliably protected in a Sync or Async manner
  across clusters
- Determining which class to use for various PVC protection schemes
- Determining a DRPolicy is of type Sync or Async
- Abstracting storage sharing/peering for Sync/Async cases from Ramen
  implementation

## Workload PVC and ManagedCluster constraints

- Workload deployed PVC should contain the SC to use, or if default SC is the
  choice then both ManagedClusters should contain the same SC as the default
    - Typically as default SC can be changed at any time, it is advisable that
      PVC carries the exact SC to use for provisioning
- Both ManagedClusters should contain the same set of SCs and should be named
  the same
    - A workload PVC would not be amended to change the SC to suit an SC that
      matches one on the peer cluster, as resource definitions come from
      declarative sources typically

## Configuration on the ManagedCluster

### StorageID

This is an identifier that can help uniquely match a StorageClass to its
respective Volume[Snapshot|Replication]Class. The StorageClass is labeled with
the StorageID and any related class that is meant to be used for PVCs
provisioned by this StorageClass is labeled with the same StorageID. This
enables mapping a PVC to its required Volume[Snapshot|Replication]Class.

If a StorageClass has multiple Volume[Snapshot|Replication]Classes, each
Volume[Snapshot|Replication]Class would carry the same StorageID. E.g two
VolumeReplicationClasses may only differ in schedule but belong to the same
StorageClass instance.

Conversely, If multiple StorageClasses share the same
Volume[Snapshot|Replication]Class, then these StorageClasses would contain the
same StorageID label.

### ReplicationID

This is an identifier that relates 2 VolumeReplicationClasses across 2
ManagedClusters as having the capability to replicate PVCs across each other.

### Labeling pre-requisite for classes on a ManagedCluster

- Label SCs with key `ramendr.openshift.io/storageID` with its appropriate
  unique `storageID` value
- Label VolumeReplicationClass with key `ramendr.openshift.io/replicationID`
  with the appropriate unique `replicationID` value
- Label Volume[Snapshot|Replication]Class with key
  `ramendr.openshift.io/storageID` with the appropriate `storageID` value

## Create ClusterClaims on the ManagedCluster

Ramen would create the following OCM [ClusterClaims](https://open-cluster-management.io/concepts/clusterclaim/)
on the ManagedCluster to share the various classes as properties on the
ManagedCluster resource at the hub

```
apiVersion: cluster.open-cluster-management.io/v1alpha1
kind: ClusterClaim
metadata:
  name: storage.class.<name>
spec:
  value: <name>
```

The metadata.name prefix value for other classes would be:

- `replication.class`
- `groupreplication.class`
- `snapshot.class`
- `groupsnapshot.class`

NOTE: From the hub listing all resources of a type (say SC) is not possible
via ManagedClusterView resource. As a result ClusterClaims help in presenting
this to the hub. Further, ClusterClaims can help reconcile dependent resources
when these change on the hub as the ManagedCluster resource change can trigger
reconcilers watching them as needed

### ClusterClaim reconciler

A new controller on the Ramen dr-cluster operator would watch for required
classes and update the corresponding ClusterClaims as needed.

## Workflow on the Hub

### DRPolicy reconciler

- Reads the ManagedCluster status for available cluster claims regarding
  classes, across the 2 DRClusters that are part of the policy
- Creates required ManagedClusterViews for the classes
- Extracts the required IDs from the classes using the Ramen labels on them
- Compares the StorageIDs and required ReplicationIDs and updates status on
  DRPolicy for entities that are common
    - A new status field for the same would be defined
      `status.[sync/async].peerClasses`
    - It would be a list containing:

    ```
    - StorageClass: <value>
      ReplicationID: <value> (optional)
      StorageID:
      - <value-C1>
      - <value-C2>
    ```

    - The list would only contain SCs that either have a matched
      Volume[Snapshot|Replication]Class across the clusters

#### Example labels to peerClasses

C1 and C2 are the peer clusters for workloads, Hub is the OCM hub cluster

C1:

- Contains the following classes and required labels
    - Name: `SClass1`
        - `ramendr.openshift.io/storageID: c1SID1`
    - Name: `SClass2`
        - `ramendr.openshift.io/storageID: c1SID2`
    - Name: `VSClass1`
        - `ramendr.openshift.io/storageID: c1SID1`
    - Name: `VRClass1`
        - `ramendr.openshift.io/storageID: c1SID1`
        - `ramendr.openshift.io/replicationID: c1RID1`
    - Name: `VSClass2`
        - `ramendr.openshift.io/storageID: c1SID2`

C2:

- Is a mirror of C1, except SIDs have the c2 prefix (SID would be different
  as these are different storage instances and refer to an async DR case)
- Contains the following in addition:
    - Name: `SClass3`
        - `ramendr.openshift.io/storageID: c2SID3`
    - Name: `SClass4`
        - No labels

Hub:

- DRPolicy `status.async.peerClasses` (or VRG `spec.async.peerClasses`)

```
- StorageClass: SClass1
  ReplicationID: c1RID1
  StorageID:
  - c1SID1
  - c2SID1
- StorageClass: SClass2
  StorageID:
  - c1SID1
  - c2SID1
```

PVCs protected:

- PVCs that use SClass1 or SClass2 will be protected by the appropriate VRG
- PVCs that use SClass3 or SClass4 will report errors as there exists no
  common replication scheme across the clusters for protection

#### Determining DRPolicy as Sync/Async

NOTE: This is a low priority requirement

For each DRPolicy:

- Compare ClassClaims for DRClusters in the policy to determine if SC names
  and StorageID matches, resulting in a Sync policy
- If they do not match, compare ClassClaims for DRClusters in the policy to
  determine if ReplicationID matches, and/or there are VolumeSnapshotClasses
  backing the StorageClasses, resulting in an Async policy
    - If Async then require a drpolicy.spec.replicationInterval configured on
      the DRPolicy

NOTE: Currently the implementation just feeds off a drcluster.spec.region string
that if equal between 2 DRClusters determines a Sync setup and if different
determines an Async setup. This will not hold up when there are multiple storage
instances on the ManagedCluster

### DRPC reconciler changes to update VRG spec

Based on the DRPolicy used by the DRPC, VRGs created will carry a supported
provisioner list in it’s spec `spec.[sync/async].peerClasses`

- VRG can only protect those PVCs for which SC values match
- VRG will have to report errors for SCs missing in the list but part of the
  workload PVCs that need protection

#### Caveats

##### Do not update VRG spec with a conflicting replication scheme

If a VRG is created already, then at the time of creation it may have defaulted
to either Volsync or VolumeReplication(Volrep) scheme of protection for a PVC
that is being protected. If in the future the DRPolicy finds a new match across
its peer clusters, and updates VRG with the new `spec.async.peerClasses`
information, on a Failover or a Relocate, specifically when the hub cluster is
also recovered, it may decide to recover the volume (or protect it in the
future) with a different scheme.

To avoid this, ensure that if VRG `spec.async.peerClasses` contains the
non-default matching class (currently Volsync, or VSC), then it will not be
updated with a new VRC that is found to be a match.

Further, delay updating the VRG `spec.async.peerClasses` with a new class
(either a VSC match or a VRC match), till it reports a PVC that cannot be
protected. This ensures that we do not advertise to the VRG a VSC initially as
that is bound to be present first, and once the clusters are peered for storage
replication, the VRC is created. As early updates in such cases would mean we
would not update the VRG with the new VRC as it would conflict with the VSC
already updated.

NOTE: For a freshly created VRG all supported classes would be updated in
`spec.async.peerClasses` as VRG would choose a default on conflict, which
currently is the Volrep

##### Example timed conflict

C1 and C2 are the peer clusters for workloads, Hub is the OCM hub cluster. T#
are time stamps.

C1 at T0:

- Contains the following classes and required labels
    - Name: SClass1
        - ramendr.openshift.io/storageID: c1SID1
    - Name: VSClass1
        - ramendr.openshift.io/storageID: c1SID1
    - Name: VSClass2
        - ramendr.openshift.io/storageID: c1SID2

C2 at T0:

Is a mirror of C1, except SIDs have the c2 prefix (SID would be different
as these are different storage instances and refer to an async DR case)

Hub at T0:

- DRPolicy `status.async.peerClasses` (or VRG `spec.async.peerClasses`)

```
- StorageClass: SClass1
  StorageID:
  - c1SID1
  - c2SID1
```

For an existing VRG, PVCs protected at T0:

- PVCs that use SClass1 will be protected by volsync

C1 (and C2) at T1, are peered at the storage layer for replication:

- Name: `VRClass1`
    - `ramendr.openshift.io/storageID: c1SID1`
    - `ramendr.openshift.io/replicationID: c1RID1`

Hub at T1:

- DRPolicy is updated to reflect the `spec.async.peerClasses[].ReplicationID`
- Existing VRGs are NOT updated with the new `ReplicationID`
    - This would prevent VRG from switching out of volsync for PVC protection
      to using volrep for the same

Additional case for delayed `peerClasses` update:

- If at T0 VRG was not protecting any PVC using `SClass2`, and `SClass2`
  appears later with only a `VSClass2`, VRG `peerClasses` would not be updated
  till there is a PVC reported as unprotected due to missing peerClasses
    - This prevents an early update to VRG, defaulting it to always use
      volsync for PVCs, as VRClass maybe setup later
- If instead at T0, a `VRClass2` is created for `SClass2`, the VRG can be
  updated with `SClass2` in its `peerClasses` with the appropriately matched
  `ReplicationID`

## Workload PVC protection on the ManagedCluster

VRG spec would now contain `spec.[sync/async].peerClasses` to enable choosing
the right mode and class within that mode of protection for PVCs

### Determining PVC as Sync/Async

When a PVC needs protection based on the VRG spec.pvcSelector,

- For Sync DR protection
    - PVCs SC should be in the list of `spec.sync.peerClasses` for Sync DR
      protection
    - Local SC as determined by the peerClasses should contain the same
      StorageID
    - This is in addition to checking that the SC in these cases are backed by
      the same storage provisioner
- For Async DR protection
    - PVCs SC should be in the list of `spec.async.peerClasses` for Async DR
      protection
    - If `spec.async.peerClasses` carries a ReplicationID then VRC determined
      based on SC StorageID, should have the same ReplicationID
    - If `spec.async.peerClasses` does not carry a ReplicationID then on the
      local cluster a VSC determined based on the SC should carry the same
      local StorageID

## Related workflows

### Hub recovery

In the case where a ManageCluster is down, its ClusterClaims may be stale. A new
workload cannot be protected at this time, which is the case at present(?). An
existing DR protected workload would have the VRG already created with the
required `spec.async.peerClasses` and should be leveraged to create/update a
VRG on a peer cluster for DRPC actions.

In cases of hub recovery when existing VRG is not found due to loss of the
ManagedCluster where the workload was placed, the values of
`spec.async.peerClasses` would need to come from the VRG in the S3 store, as the
current hub cannot reform the ClusterClaims due to lost ManagedCluster.

### New Class with labels on the ManagedCluster(s)

If a new class is added to a ManagedCluster this will result in,

- A new ClusterClaim and hence a new ClassClaim
- All DRPCs that refer to the cluster(s) that have a new claim can modify the
  VRG `spec.async.peerClasses` appropriately, this will enable protecting
  newer PVCs by these VRGs in case they are from one of the newer classes
- NOTE: As VRG generation will change in this case, a fresh PVC upload would
  be triggered, there should be no other impact due to VRG as Primary changing
  generations

### Changes for VolumeGroup[Snapshot|Replication]Class

As we would also start supporting the group classes, the changes to peerClasses
would look like so:

```
- StorageClass: SClass1
  StorageID:
  - c1SID1
  - c2SID1
  replicationGroup: true
  snapshotGroup: true
```

The `replicationGroup` would be set to true if both peers have a matching
VolumeGroupReplicationClass with the same ReplicaitonID

The `snapshotGoup` would be set to true if both peers have a
VolumeGroupSnapshotClass referenced by the StorageClass StorageID

### Cross storage provider DR

Although this is not a use-case that the ClassClaims aim to resolve, it is added
here for potential future possibilities based on the class labels discussed
here.

ManagedClusters that are peered can label arbitrary classes (i.e backed by
different provisioners/storage backends) with the right Ramen labels, for
volsync based DR protection to sync contents across different storage providers.

E.g Say EastCluster has SCWorkload and VSCEast using ProviderA and WestCluster
has its corollary SCWorkload, VSCWest, etc. and it is desired that volumes be
synced across these providers then,

- Labeling the classes with the prescribed Ramen labels and unique values will
  allow ramen to match these classes are peers when moving PVCs across
  clusters
- The VSCs would also need the appropriate labeling for volsync based
  orchestration to match the right VSC on the ManagedCluster when creating the
  required volsync Replication[Source|Destination]

## Implementation notes

Implementation would proceed along the following lines:

- Initially assume peer clusters are configured correctly, and work on DRPC
  updating peer VRG `spec.[sync|async].peerClasses` based on the IDs carried
  in the `status.protectedPVCs`, for any action
- Work on ClusterClaim controller at the dr-cluster operator
- Work on DRPolicy leveraging cluster claims from the ManagedCluster hub
  resource, to update DRPolicy with `status.[sync|async].peerClasses` (this
  can be done independently by creating ClusterClaims using the CLI rather
  than waiting for the dr-cluster controller to do the same)
- Work on DRPC leveraging DRPolicy to create VRGs with the relevant
  `spec.[sync|async].peerClasses`
- Work on reading VRG from s3 store on hub recovery cases
