# Design notes for grouping volumes for consistency group based DR

## Grouping decision

DRPC will carry an additional `spec.group` (or, `spec.async.group`) boolean,
denoting if grouping of volumes is desired for consistency group (CG) based
protection. VRG would also carry a similar spec field, that would be populated
by the DRPC reconciler.

- This is to facilitate upgrades from existing single PVC protection to
  protecting PVCs via consistency groups.
    - This field would be immutable, and hence existing DRPCs would default to
      non-group behavior
    - To shift to grouping behavior, the workload needs to be disabled for DR
      protection and reenabled for DR protection with the grouping set to true
- This also mandates that all PVCs SHOULD be grouped and hence enables VRG to
  strictly group PVCs
    - Any PVC that cannot be added to a CG would hence fail protection for the
      entire workload

## PVC grouping labels

Both VolumeGroupSnapshot and VolumeGroupReplication definitions require a PVC
label selector that can uniquely identify a group of PVCs that belong to the
request. PVCs in this group should be an exact set belonging to a storage
instance, such that the backing CSI implementation can snapshot/replicate the
list of volume handles that are passed to it.

This requires that PVCs that are to be protected, based on the VRG/DRPC
`spec.pvcSelector` needs an additional label to separate this set into disjoint
sets of PVCs, when they are provisioned by different storage instances.

For the purposes of this grouping the `storageID`, that is matched across the
StorageClass that the PVC is referring to with the VolumeGroupSnapshotClass,
would be used to label the PVCs. (see storageID related design for properties of
this label).

The proposed label name is `ramendr.openshift.io/consistency-group`, with the
value being the `storageID`.

The VGS or the VGR label selector would be an AND of the `spec.pvcSelector` and
the `ramendr.openshift.io/consistency-group` label with its specific `storageID`
value.

## DRPC status enhancements

DRPC status for ProtectedPVCs would be enhanced to support information regarding
which PVCs are grouped and protected within a CG. The current
`status.resourceConditions.resourceMeta` would be enhanced as follows,

```
protectedPVCs: []string
protectedGroups: []PVCGroups

PVCGroups:
    - name: string
    - protectedPVCs: []string
```

When a DRPC opts into group support `resourceMeta.protectedPVCs` would be empty,
and all information would be present in `resourceMeta.protectedGroups` and
vice versa.

TODO: A PVCGroup needs a `name`, which can be extended based on the backend
created VGS or VGR request for the group.

## Metrics and monitoring

VRG currently reports a `lastGroupSyncTime` status, which is the oldest
timestamp of sync time across all PVCs that are protected by VRG. This includes
PVCs protected by volsync or volrep scheme.

With strict grouping, each group would report a similar `lastGroupSyncTime` and
the same status field would report the oldest sync time as the point of
synchronization.

Similar scheme would apply to the `lastSyncDuration` and `lastSyncBytes` status
fields.

DRPC, as is currently, would pick these fields and convert them to respective
metrics and alerts from these metrics.

## Limitations

1. A workload that has PVCs sub-grouped, due to being provisioned from different
  storage instances or types, would only maintain consistency of the group and not
  consistency of volumes across these sub-groups.

  For example, if a workload contains a PVC protected by a volsync group and
  another protected by a volrep group, the consistency of data across these groups
  are independent of each other.
1. ...
