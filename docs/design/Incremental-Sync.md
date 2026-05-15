<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Incremental Sync for Consistency Groups

Extends [Cephfs-RDR-ConsistencyGroup.md](Cephfs-RDR-ConsistencyGroup.md).

## Summary

When `drplacementcontrol.ramendr.openshift.io/enable-diff: "true"` is set on a DRPC,
Ramen preserves VolumeGroupSnapshots across sync cycles and uses the
[ceph-volsync-plugin](https://github.com/RamenDR/ceph-volsync-plugin) External mover to
transfer only changed blocks between consecutive snapshots.

## Annotation Propagation

```
DRPC → VRG → RGS (ReplicationGroupSource)
           → RGD (ReplicationGroupDestination)
```

Follows the same flow as `UseVolSyncAnnotation`: the user sets the annotation on the
DRPC instance, which propagates it to the VRG via ManifestWork. The annotation is
whitelisted in `constructVRGFromView` so it survives hub round-trips. DRPC annotation
changes trigger reconcile automatically via the default `For(&DRPlacementControl{})`
predicate.

Checked via `util.IsDiffSyncEnabled()`. Value must be exactly `"true"`.

## VolumeGroupSnapshot Lifecycle

Standard handler: single VGS with fixed name, deleted each cycle.

Diff handler: maintains two VGS via label `ramen.openshift.io/vgs-status`:

| Phase   | Action                                                     |
| ------- | ---------------------------------------------------------- |
| Create  | New VGS `{rgs-name}-{timestamp}`, `status=current`         |
| Cleanup | Prune old previous, delete restored PVCs, current→previous |

At rest: exactly one VGS with `status=previous` serves as base for next cycle.

```
Cycle N:   [VGS-N: current] → sync → [VGS-N → previous]
Cycle N+1: [VGS-N+1: current] → diff(VGS-N, VGS-N+1) → [VGS-N deleted, VGS-N+1 → previous]
```

## External Spec vs RsyncTLS

Diff sync replaces the standard RsyncTLS ReplicationSource spec with an External spec:

```go
rs.Spec.External = &ReplicationSourceExternalSpec{
    Provider: storageClass.Provisioner,
    Parameters: map[string]string{
        "copyMethod":         "Direct",
        "volumeName":         sourcePVCName,
        "baseSnapshotName":   previousSnapshotName,  // from previous VGS
        "targetSnapshotName": currentSnapshotName,    // from current VGS
        "address":            rdService,
        "keySecret":          volsyncPSKSecretName,
    },
}
```

ReplicationDestination similarly uses External with `copyMethod=Snapshot`.

The ceph-volsync-plugin mover compares `baseSnapshotName` and `targetSnapshotName` at
block level using CephFS snapshot diff APIs, transferring only changed blocks. When
`baseSnapshotName` is empty (first cycle), it falls back to full sync.

## Handler Method Overrides

`diffVolumeGroupSourceHandler` embeds `volumeGroupSourceHandler` and overrides four
methods:

| Method            | Standard           | Diff                      |
| ----------------- | ------------------ | ------------------------- |
| CreateOrUpdateVGS | Fixed name, single | Timestamp suffix, labels  |
| CleanVGS          | Delete VGS + PVCs  | Preserve previous, rotate |
| RestoreFromVGS    | Lookup by name     | Lookup by status label    |
| CreateOrUpdateRS  | RsyncTLS spec      | External spec + params    |

All other methods inherited: `RestoreVolumesFromSnapshot`,
`CheckReplicationSourceForRestoredPVCsCompleted`, `WaitIfPVCTooNew`,
`EnsureApplicationPVCsMounted`.

## Failover Rollback (CopyMethodDirect)

When `CopyMethodDirect` is used, the RD syncs directly into the app PVC. On failover the
PVC may have partial data from an interrupted sync and must be rolled back to the last
known good snapshot.

Standard path: local RD + local RS with RsyncTLS copy the entire LatestImage snapshot
into the app PVC (full copy).

Diff sync path: replaces the full copy with a block-level diff rollback using the
ceph-volsync-plugin External spec.

### Flow

```
rollbackToLastSnapshot()
  1. Pause main RD
  2. Create current-state snapshot of app PVC
  3. Wait for snapshot ready (return-and-retry)
  4. Create local RD (External, CopyMethodDirect, destPVC=appPVC)
  5. Wait for RD address (plugin populates Status.RsyncTLS.Address)
  6. Create shallow PVC from LatestImage snapshot
  7. Create local RS (External) with diff params
  8. Wait for sync completion
  9. Pause local RD
 10. Cleanup: delete current-state snapshot, shallow PVC, local RS/RD
```

### Local RS External Spec

```go
lrs.Spec.External = &ReplicationSourceExternalSpec{
    Provider: storageClass.Provisioner,
    Parameters: map[string]string{
        "copyMethod":         "Direct",
        "volumeName":         appPVCName,
        "baseSnapshotName":   currentStateSnapshotName,
        "targetSnapshotName": latestImageSnapshotName,
        "address":            localRDServiceAddress,
        "keySecret":          volsyncPSKSecretName,
    },
}
```

The plugin diffs `baseSnapshotName` (current corrupted state) against
`targetSnapshotName` (last known good) and writes only the changed blocks to
`volumeName`, rolling the PVC back efficiently.

## DR Operations

Transparent to failover/relocate. After failover, first sync on the new primary is full
(no previous base); subsequent syncs are incremental.

## Dependencies

- VolumeGroupSnapshot CSI
- ceph-volsync-plugin with External mover support
