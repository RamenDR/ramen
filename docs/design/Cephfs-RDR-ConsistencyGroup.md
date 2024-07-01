# CephFS Regional DR Consistency Group

## Summary

Currently, Ramen only supports non-consistency-group CephFS RDR
(Regional Disaster Recovery). However, with the upcoming support
for consistency groups in CephFS, Ramen can expand its capabilities
to include consistency-group RDR for CephFS.

## Motivation

Consistency Groups are pivotal in maintaining data integrity during
synchronizing related datasets across regions, mitigating
the risk of data corruption or loss during failover events. They
constitute essential components of Regional Disaster Recovery, enabling
organizations to minimize risks and ensure the availability and integrity
of their data across geographical locations.

### Goals

* Support Crash Consistency Group in RDR failover
* Support Application Consistency Group in RDR relocate

### Non-Goals

* Consistency Group in MDR (Metro Disaster Recovery)
* Consistency Group for RBD (Rados Block Device)
* Application cross Multi-Namespaces

## Design Details

Currently, Ramen utilizes volsync to capture snapshots for
each PVC in the application and synchronize the restored data
to a remote destination. However, volsync lacks the capability
to take a volume group snapshot for PVCs in a consistency group
and sync the restored data to the destination.

In this new design, Ramen will capture a volume group snapshot
for the volumes within a consistency group and restore the
snapshot to new read-only PVCs. Subsequently, Ramen will use
volsync to synchronize the data in the read-only PVCs to the remote destination.

### User Story

For the application, All Cephfs PVCs belong to a single consistency group.

### Does enabling the feature change any default behavior

No, this design will only be executed after enabling consistency group.

### Dependencies

* CephFS VolumeGroupSnapshot CSI

### New API

To support Volume Group during Regional DR,
two additional CRDs have been introduced:

* RGS (ReplicationGroupSource): This resource functions similarly
  to volsync ReplicationSource, managing Volume Groups as a source.

```go
type ReplicationGroupSource struct {
  metav1.TypeMeta   `json:",inline"`
  metav1.ObjectMeta `json:"metadata,omitempty"`

  Spec   ReplicationGroupSourceSpec   `json:"spec,omitempty"`
  Status ReplicationGroupSourceStatus `json:"status,omitempty"`
}

type ReplicationGroupSourceSpec struct {
    Trigger *ReplicationSourceTriggerSpec `json:"trigger,omitempty"`

    // +required
    VolumeGroupSnapshotClassName string `json:"volumeGroupSnapshotClassName,omitempty"`

    // +required
    VolumeGroupSnapshotSource *metav1.LabelSelector `json:"volumeGroupSnapshotSource,omitempty"`
}

// ReplicationSourceTriggerSpec defines when a volume will be synchronized with
// the destination.
type ReplicationSourceTriggerSpec struct {
    Schedule *string `json:"schedule,omitempty"`
    Manual string `json:"manual,omitempty"`
}

// ReplicationGroupSourceStatus defines the observed state of ReplicationGroupSource
type ReplicationGroupSourceStatus struct {
  // lastSyncTime is the time of the most recent successful synchronization.
  //+optional
  LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
  // lastSyncStartTime is the time the most recent synchronization started.
  //+optional
  LastSyncStartTime *metav1.Time `json:"lastSyncStartTime,omitempty"`
  // lastSyncDuration is the amount of time required to send the most recent
  // update.
  //+optional
  LastSyncDuration *metav1.Duration `json:"lastSyncDuration,omitempty"`
  // nextSyncTime is the time when the next volume synchronization is
  // scheduled to start (for schedule-based synchronization).
  //+optional
  NextSyncTime *metav1.Time `json:"nextSyncTime,omitempty"`
  //+optional
  LastManualSync string `json:"lastManualSync,omitempty"`
  // conditions represent the latest available observations of the
  // source's state.
  Conditions []metav1.Condition `json:"conditions,omitempty"`  
  // Created ReplicationSources by this ReplicationGroupSource
  ReplicationSources []*corev1.ObjectReference `json:"replicationSources,omitempty"`
}
```

* RGD (ReplicationGroupDestination): This resource functions similarly
  to volsync ReplicationDestination, managing Volume Groups as a destination.

```go
type ReplicationGroupDestination struct {
  metav1.TypeMeta   `json:",inline"`
  metav1.ObjectMeta `json:"metadata,omitempty"`

  Spec   ReplicationGroupDestinationSpec `json:"spec,omitempty"`
  Status *ReplicationDestinationStatus   `json:"status,omitempty"`
}

type ReplicationGroupDestinationSpec struct {
  // Label selector to identify the VolumeSnapshotClass resources
    // that are scanned to select an appropriate VolumeSnapshotClass
    // for the VolumeReplication resource when using VolSync.
    //+optional
    VolumeSnapshotClassSelector metav1.LabelSelector `json:"volumeSnapshotClassSelector,omitempty"`

  // RDSpecs specify the PVCs in a volume group
  RDSpecs []VolSyncReplicationDestinationSpec `json:"rdspecs,omitempty"`
}

type ReplicationDestinationStatus struct {
    // lastSyncTime is the time of the most recent successful synchronization.
    //+optional
    LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
    // lastSyncStartTime is the time the most recent synchronization started.
    //+optional
    LastSyncStartTime *metav1.Time `json:"lastSyncStartTime,omitempty"`
    // lastSyncDuration is the amount of time required to send the most recent
    // update.
    //+optional
    LastSyncDuration *metav1.Duration `json:"lastSyncDuration,omitempty"`
    // nextSyncTime is the time when the next volume synchronization is
    // scheduled to start (for schedule-based synchronization).
    //+optional
    NextSyncTime *metav1.Time `json:"nextSyncTime,omitempty"`
    // conditions represent the latest available observations of the
    // source's state.
    Conditions []metav1.Condition `json:"conditions,omitempty"`
    // latestImage in the object holding the most recent consistent replicated
    // image.
    //+optional
    LatestImages map[string]*corev1.TypedLocalObjectReference `json:"latestImage,omitempty"`
    // Created ReplicationDestinations by this ReplicationGroupDestination
    ReplicationDestinations []*corev1.ObjectReference `json:"replicationDestinations,omitempty"`
}
```

### Naming

* RS (ReplicationSource): A VolSync resource designed to
  manage the source. Each RS is responsible for managing one PVC as the data source.
* RGS (ReplicationGroupSource): A Ramen resource designed to
  manage all RS instances in a consistency group.
* RD (ReplicationDestination): A VolSync resource intended
  for managing the destination. Each RD handles one PVC as the data destination.
* RGD (ReplicationGroupDestination): A Ramen resource
  designed to manage all RD instances in a consistency group.

The naming follow the volsync handler. Currently, in volsync handler:

* the ReplicationSource and ReplicationDestination
  have the same name with Application PVC name
* the snapshot name of source application pvc is volsync-`PVC_NAME`-src
* the name of tmp pvc restored by volsync is volsync-`PVC_NAME`-src

In this design, ReplicationGroupSource create VolumeGroupSnapshot,
Restored PVC and ReplicationSource in each sync.
At the end of each sync, VolumeGroupSnapshot, Restored PVC will be deleted by ramen,
ReplicationSource will not be deleted.

* ReplicationGroupSource Name = ReplicationGroupDestination Name
  = `VRG Name` + `cgName` = `Application Name` + `cgName`
* VolumeGroupSnapshot Name = cephfscg-`ReplicationGroupSource Name`
* Restored PVC Name = cephfscg-`Application PVC Name`
* ReplicationSource Name = ReplicationDestination Name = `Application PVC Name`
* ReplicationDestinationServiceName =
  volsync-rsync-tls-dst-`Application PVC Name`.`RD Namespace`.svc.clusterset.local
* Volsync Secret Name = `VRG Name`-vs-secret
  ReplicationGroupDestination will create application PVC
  which is the same with current implementation.

### DR Enable

* RS (ReplicationSource): A VolSync resource designed to
  manage the source. Each RS is responsible for managing one PVC as the data source.
* RGS (ReplicationGroupSource): A Ramen resource designed to
  manage all RS instances in a consistency group.
* RD (ReplicationDestination): A VolSync resource intended
  for managing the destination. Each RD handles one PVC as the data destination.
* RGD (ReplicationGroupDestination): A Ramen resource
  designed to manage all RD instances in a consistency group.

![RDR Enable](../diagrams/cephfs-cg-rdr-enable.drawio.svg)

This diagram illustrates the design for enabling CephFS
Regional Disaster Recovery (Regional-DR) with Consistency Group.
To highlight the changes from this design to the current implementation,
modifications have been appended to the bottom of the diagram
rather than altering the original version.

In step 6, currently, the PVCs in vrg are triaged into cephFS and RBD.
To support consistency group, if a consistency group is specified,
the cephFS PVCs also need to be further triaged into consistency group.

In step 10, currently, Ramen creates a ReplicationSource for each PVC
to enable Volsync to create independent volume snapshots for each PVC.
Subsequently, Volsync restores each independent snapshot to a new
read-only PVC to synchronize the data from it to the remote destination.

In this design, since volsync doesn't currently support consistency groups,
Ramen takes on the responsibility of creating volume group snapshots for
the PVCs within the consistency group and restoring these snapshots to PVCs.
Subsequently, Volsync synchronizes the data from the PVCs restored by
Ramen to the remote destination.

So, in step 10, there are some differences compared to the
current implementation if a consistency group is specified:

* Ramen get the existing VolumeGroupSnapshotClass
* Ramen creates a ReplicationGroupSource with the schedule
  specification from the primary VRG. **This implies that Ramen will
  have a scheduler, replacing VolSync scheduler, to manage the
  synchronization scheduling of PVCs within a consistency group
  to the destination.** Within each schedule:
    * Ramen Update Status.LastSyncStartTime. Go to the Syncing state.
      In Syncing state:
        * Ramen creates a VolumeGroupSnapshot for the consistency group.
          This VolumeGroupSnapshot generate VolumeSnapshots for each PVC
          within the consistency group, all these VolumeSnapshots
          within the same consistency group.
        * Each snapshot is then restored to a new read-only PVC by Ramen
        * Ramen creates a ReplicationSource (Use Manual and Direct
          method in ReplicationSource) for each restored PVC. In next schedule,
          Ramen updates the existing ReplicationSource with new manual string
          and restored PVC.
        * Ramen monitors the manual string in the ReplicationSource
          spec and status to ensure all PVCs in the group are
          successfully synced to the remote destination.
        * Once synchronization is completed, update Status.LastSyncStartTime
          to nil, and Status.LastSyncTime. Go to Cleanup state
    * In Cleanup state:
        * Ramen cleans up each read-only PVCs that were restored from the snapshot.
        * It also cleans up the VolumeGroupSnapshot created earlier.
        * Ramen **DO NOT** delete the ReplicationSource maintaining its status information.
        * Update Status.LastSyncStartTime to now. Go to Syncing state

In step 12, volsync will sync the data in the new restored read-only PVC
(restored by Ramen) to the remote destination

In step 18, Ramen will first create a ReplicationGroupDestination,
which include the RD spec for each PVC in the consistency group.

* Ramen Update Status.LastSyncStartTime. Go to the Syncing state.
  In Syncing state:
    * Create Or Update manual trigger ReplicationDestination for each PVC
    * Wait all RDs are successfully to receive the data from source by
      checking the manual string in spec and status
    * Get latest image from each RD and update the Status.LatestImages
      in the ReplicationGroupDestination
    * Update Status.LastSyncStartTime to nil, and Status.LastSyncTime.
      Go to Cleanup State
* In Cleanup Sate:
    * Update Status.LastSyncStartTime to now to go to Syncing state

### Failover

* RS (ReplicationSource): A VolSync resource designed to manage the source.
  Each RS is responsible for managing one PVC as the data source.
* RGS (ReplicationGroupSource): A Ramen resource designed to manage all
  RS instances in a consistency group.
* RD (ReplicationDestination): A VolSync resource intended for managing the
  destination. Each RD handles one PVC as the data destination.
* RGD (ReplicationGroupDestination): A Ramen resource designed to
  manage all RD instances in a consistency group.

![RDR Failover](../diagrams/cephfs-cg-rdr-failover.drawio.svg)

This diagram illustrates the design for CephFS Regional-DR with
Consistency Group failover.
The modifications have been appended to the bottom of the diagram
rather than altering the original version.

In the current implementation, during step 6, the application's
PVCs are restored to the
latest image from their corresponding Replication Destination.

In this design, we record the image in consistency group in the ReplicationGroupDestination.
So we need to get the latest image from ReplicationGroupDestination
instead of each ReplicationDestination.

In step 9, additionally, the ReplicationGroupDestination need to be cleaned.

In step 10, currently, Ramen creates a ReplicationSource for each PVC
to enable Volsync to create independent volume snapshots for each PVC.
Subsequently, Volsync restores each independent snapshot to a new
read-only PVC to synchronize the data from it to the remote destination.

In this design, since volsync doesn't currently support consistency groups,
Ramen takes on the responsibility of creating volume group snapshots for
the PVCs within the consistency group and restoring these snapshots to PVCs.
Subsequently, Volsync synchronizes the data from the PVCs restored by
Ramen to the remote destination.

So, in step 10, there are some differences compared to the
current implementation if a consistency group is specified:

* Ramen get the existing VolumeGroupSnapshotClass
* Ramen creates a ReplicationGroupSource with the schedule
  specification from the primary VRG. **This implies that Ramen will
  have a scheduler, replacing VolSync scheduler, to manage the
  synchronization scheduling of PVCs within a consistency group
  to the destination.** Within each schedule:
    * Ramen Update Status.LastSyncStartTime. Go to the Syncing state.
      In Syncing state:
        * Ramen creates a VolumeGroupSnapshot for the consistency group.
          This VolumeGroupSnapshot generate VolumeSnapshots for each PVC
          within the consistency group, all these VolumeSnapshots
          within the same consistency group.
        * Each snapshot is then restored to a new read-only PVC by Ramen
        * Ramen creates a ReplicationSource (Use Manual and Direct
          method in ReplicationSource) for each restored PVC. In next schedule,
          Ramen updates the existing ReplicationSource with new manual string
          and restored PVC.
        * Ramen monitors the manual string in the ReplicationSource
          spec and status to ensure all PVCs in the group are
          successfully synced to the remote destination.
        * Once synchronization is completed, update Status.LastSyncStartTime
          to nil, and Status.LastSyncTime. Go to Cleanup state
    * In Cleanup state:
        * Ramen cleans up each read-only PVCs that were restored from the snapshot.
        * It also cleans up the VolumeGroupSnapshot created earlier.
        * Ramen **DO NOT** delete the ReplicationSource maintaining its status information.
        * Update Status.LastSyncStartTime to now. Go to Syncing state

In step 12, volsync will sync the data in the new restored read-only PVC
(restored by Ramen) to the remote destination

In step 22, Ramen will first create a ReplicationGroupDestination,
which include the RD spec for each PVC in the consistency group.

* Ramen Update Status.LastSyncStartTime. Go to the Syncing state.
  In Syncing state:
    * Create Or Update manual trigger ReplicationDestination for each PVC
    * Wait all RDs are successfully to receive the data from source by
      checking the manual string in spec and status
    * Get latest image from each RD and update the Status.LatestImages
      in the ReplicationGroupDestination
    * Update Status.LastSyncStartTime to nil, and Status.LastSyncTime.
      Go to Cleanup State
* In Cleanup Sate:
    * Update Status.LastSyncStartTime to now to go to Syncing state

### Relocate

* RS (ReplicationSource): A VolSync resource designed to manage the source.
  Each RS is responsible for managing one PVC as the data source.
* RGS (ReplicationGroupSource): A Ramen resource designed to manage all RS
  instances in a consistency group.
* RD (ReplicationDestination): A VolSync resource intended for managing the
  destination. Each RD handles one PVC as the data destination.
* RGD (ReplicationGroupDestination): A Ramen resource designed to manage
  all RD instances in a consistency group.

![DR Relocate](../diagrams/cephfs-cg-rdr-relocate.drawio.svg)

This diagram illustrates the design for CephFS Regional-DR with
Consistency Group relocate. The modifications have been appended
to the bottom of the diagram rather than altering the original version.

In step 2.6, currently, Ramen will use the manaul way to do a
final sync for each ReplicationSource. In this design, we will
do a final sync for the ReplicationGroupSource.

In step 9, additionally, the ReplicationGroupDestination need to be cleaned.

In step 10, currently, Ramen creates a ReplicationSource for each PVC
to enable Volsync to create independent volume snapshots for each PVC.
Subsequently, Volsync restores each independent snapshot to a new
read-only PVC to synchronize the data from it to the remote destination.

In this design, since volsync doesn't currently support consistency groups,
Ramen takes on the responsibility of creating volume group snapshots for
the PVCs within the consistency group and restoring these snapshots to PVCs.
Subsequently, Volsync synchronizes the data from the PVCs restored by
Ramen to the remote destination.

So, in step 10, there are some differences compared to the
current implementation if a consistency group is specified:

* Ramen get the existing VolumeGroupSnapshotClass
* Ramen creates a ReplicationGroupSource with the schedule
  specification from the primary VRG. **This implies that Ramen will
  have a scheduler, replacing VolSync scheduler, to manage the
  synchronization scheduling of PVCs within a consistency group
  to the destination.** Within each schedule:
    * Ramen Update Status.LastSyncStartTime. Go to the Syncing state.
      In Syncing state:
        * Ramen creates a VolumeGroupSnapshot for the consistency group.
          This VolumeGroupSnapshot generate VolumeSnapshots for each PVC
          within the consistency group, all these VolumeSnapshots
          within the same consistency group.
        * Each snapshot is then restored to a new read-only PVC by Ramen
        * Ramen creates a ReplicationSource (Use Manual and Direct
          method in ReplicationSource) for each restored PVC. In next schedule,
          Ramen updates the existing ReplicationSource with new manual string
          and restored PVC.
        * Ramen monitors the manual string in the ReplicationSource
          spec and status to ensure all PVCs in the group are
          successfully synced to the remote destination.
        * Once synchronization is completed, update Status.LastSyncStartTime
          to nil, and Status.LastSyncTime. Go to Cleanup state
    * In Cleanup state:
        * Ramen cleans up each read-only PVCs that were restored from the snapshot.
        * It also cleans up the VolumeGroupSnapshot created earlier.
        * Ramen **DO NOT** delete the ReplicationSource maintaining its status information.
        * Update Status.LastSyncStartTime to now. Go to Syncing state

In step 12, volsync will sync the data in the new restored read-only PVC
(restored by Ramen) to the remote destination

In step 22, Ramen will first create a ReplicationGroupDestination,
which include the RD spec for each PVC in the consistency group.

* Ramen Update Status.LastSyncStartTime. Go to the Syncing state.
  In Syncing state:
    * Create Or Update manual trigger ReplicationDestination for each PVC
    * Wait all RDs are successfully to receive the data from source by
      checking the manual string in spec and status
    * Get latest image from each RD and update the Status.LatestImages
      in the ReplicationGroupDestination
    * Update Status.LastSyncStartTime to nil, and Status.LastSyncTime.
      Go to Cleanup State
* In Cleanup Sate:
    * Update Status.LastSyncStartTime to now to go to Syncing state

### State Machine

Both ReplicationGroupSource and ReplicationGroupDestination
operate as instances of a state machine:

![sm](../diagrams/cephfs-cg-sm.drawio.svg)

This State Machine utilizes a combination of `Status.LastSyncTime`
and `Status.LastSyncStartTime` to determine its state:

* If both `Status.LastSyncTime` and `Status.LastSyncStartTime` are empty,
  the machine is in the Initial State:
    * The machine sets `Status.LastSyncStartTime` to the current time,
      then it will go to the Syncing State
* If `Status.LastSyncStartTime` is not empty, the machine is in Syncing State
    * In Syncing State, machine invokes the Synchronize function and
      clears `Status.LastSyncStartTime`. It then transitions to the Cleanup State.
* Otherwise, it's in Cleanup State.
    * In Cleanup State, machine calls Cleanup function and sets
      `Status.LastSyncStartTime` to the current time.
      It then transitions back to the Syncing State.
