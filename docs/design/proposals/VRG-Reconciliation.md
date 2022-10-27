<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# VRG reconciler workflow

This design document covers the VRG CRD and its reconciliation workflow.

## Terminology

- VRG: VolumeReplicationGroup
- VR: VolumeReplication
- PVC: PersistentVolumeClaim
- PV: PersistentVolume

## VRG operator functionality

VRG resource exists to manage a group PVCs that need replication. It is a
namespace level resource that ensures the following:

- For each existing or dynamically created PVC:
    - Ensures PVCs are protected from deletion, prior to replicating them
    - Ensures PVCs are bound before enabling replication for them
    - Ensures only PVCs that are NOT being deleted are protected, when
    reconciling them
    - Ensures PVs backing the PVCs are retained for the duration that they
    are protected
    - Backs up the PV cluster data to an external S3 store, to enable restoring
    them on a remote peer cluster
- Creates and manages VR resource for PVCs that need to be protected
- Manages state changes in VRG by ensuring VRs reach the same desired state,
  coordinating with PVC state and ensuring:
    - PVCs are not in use when being marked Secondary
    - PVCs are marked for deletion when being marked Secondary
- Manages VRG deletion, by ensuring VRs are in the desired state prior to
  deletion

## VRG CRD

### Spec

```
apiVersion: ramendr.openshift.io/v1alpha1
kind: VolumeReplicationGroup
metadata:
  name: vrg-sample
  generation: <generation>
spec:
  pvcSelector:
    matchLabels:
      <key>: <value>
  volumeReplicationClass: <string>
  replicationState: "Primary"|"Secondary"
  s3Endpoint: <url-string>
  s3SecretName: <string>
```

- Fields:
    - PVCSelector
        - Label selector to identify all the PVCs that are belong to this VRG
        and need to be replicated
    - VolumeReplicationClass
        - ReplicationClass for all volumes in this replication group; this
        value is propagated to created VolumeReplication CRs
    - ReplicationState
        - Desired state of all volumes [`primary` or `secondary`] in this
        replication group; this value is propagated to created
        VolumeReplication CRs
    - S3Profiles
        - S3 Profiles to replicate PV cluster data

### Status

```
status:
  protectedPVCs: [
    pvcName: <string>
    conditions: [
      type: ["Available"]
      status: [true|false|unknown]
      observedGeneration: <generation>
      lastTransitionTime: <time>
      reason: "Error"|"Progressing"|"Replicating"
      message: <human readable string>
    ]
  ]
  conditions: [
    type: ["Available"]
    status: [true|false|unknown]
    observedGeneration: <generation>
    lastTransitionTime: <time>
    reason: ["Replicating"|"Progressing"|"Error"]
    message: <human readable string>
  ]
```

- `status.conditions`:
    - `type` "Available" is to denote availability status of the VRG resource
        - **NOTE:** `status` may change back to "Progressing" in case there
        are new PVCs to deal with for the same CRD `generation`
    - `status`
        - "Unknown" if VRG is not yet picked up for reconciliation
        - "True" if reconciliation is completed
            - `reason` is "Replicating"
        - "False" if reconciliation is progressing or has errors
            - `reason`: Progressing|Error
- `status.protectedPVCs`:
    - `pvcName`: Name of the PVC for which conditions are represented
    - `conditions`:
        - `type` "Available" is to denote availability status for PVC
        protection
        - `status`
            - "True" if `reason` is  "Replicating"
            - "False" if `reason` is one of "Error" or "Progressing", to
            denote any hard errors or that reconciliation is in progress
        - **NOTE(s):**
            - Ideally if status.conditions.[Available].status is True, the all
            PVC status should be true, with the reason as "Replicating"
            - An `observedGeneration` number for when the PVC was protected
            may not be required, as once it is protected it stays protected.
            It may prove useful though if any straggling PVCs were protected
            during a cluster heal or related operations, to detect prior
            failover PVCs with the current generation

#### Notes

- Status may change for the same observed generation, as PVCs are created
- Should there be a per PVC condition that it's backing PV was backed up?
  To denote that replication at storage maybe missing, but PV cluster data may exist?

### VRG Example

```
---
apiVersion: ramendr.openshift.io/v1alpha1
kind: VolumeReplicationGroup
metadata:
  name: vrg-sample
spec:
  pvcSelector:
    matchLabels:
      appname: myapp
  volumeReplicationClass: "vr-class-sample"
  replicationState: "Primary"
  s3Profiles:
    s3ProfileEast
    s3ProfileWest
```

## VRG reconciliation

### PVC preparation and protection for all states

All reconciliation state changes need to ensure a PVC is in the right state
and protected prior to proceeding with the state change. The following steps
are taken to ensure the same:

1. Check if PVC is already processed by, checking if `vr-protected` annotation
  is already present on the PVC, if so move to step 3.8
1. If PVC `phase` is not [bound](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#phase)
  , continue to the next PVC, but requeue for reconciliation
    - PVC should be bound, to enable replication of the bound PV
1. Check if `pvc-vr-protection` finalizer is not yet set on the PVC and if
  PVC is being deleted:
    1. If true, skip the current PVC and continue to the next PVC
        - A PVC that is being deleted and not yet protected can be skipped
1. Add `pvc-vr-protection` finalizer to PVC, to enabling ordering of VR
  deletion before PVC deletion
    1. **NOTE:** VR would be deleted when VRG is deleted, and VRG is also
    protected by its own finalizer `vrg-protection` for such ordering
1. Change bound PV's `reclaimPolicy` to "Retain", if not already "Retain" and
  add an annotation `vr-retained` to remember VRG processing did the change
    - Bound PV needs to be retained, till the PVC is deleted by the user on
    the cluster where it is "Primary", this ensures underlying storage volume
    is retained across the DR instances when replicated
1. Backup Bound PV's cluster data
1. Finally, add `vr-protected` annotation to PVC, to enable optimizing repeat
  of steps above

### Reconciling VRG as Primary

1. Add `vrg-protection` finalizer to VRG instance
1. Select all PVCs that match `PVCSelector` for processing
1. For each PVC
    1. Prepare and protect PVC as [above](#pvc_preparation_and_protection_for_all_states)
    1. [Find|Create] VR for PVC as `primary`
1. For each protected PVC
    1. Check if VR status has reached `primary`
        1. If not, update status as progressing
1. Report status as completed, if not progressing or error for any PVC

#### Notes

- **NOTE:** Current assumption is that if a PV changes size (which is possibly
  the only change to PV as of now), it need not be backed up again, as the PVC
  size would attempt to ensure a resize on a peer cluster and the local PV
  instance on that cluster should catch up to the actual PV size without having
  to backup or restore the same.
    - Maybe, we could do an introspection of the old and new PV as in the
    function [`pvcPredicateFunc`](https://github.com/RamenDR/ramen/blob/90b4d8d853c8ac8908b27a4517a95879b4ee597d/controllers/volumereplicationgroup_controller.go#L112-L130)
    and back this PV up again, if the assumption does not hold

### Reconciling VRG as Secondary

1. Add `vrg-protection` finalizer to VRG instance
1. Select all PVCs that match `PVCSelector` for processing
1. For each PVC
    1. Prepare and protect PVC as [above](#pvc_preparation_and_protection_for_all_states)
    1. Ensure PVC is not in use, and is deleted, if not requeue
      reconciliation for this PVC
        - PVC in use can dump more data to the volume, and if not ordered
        prior to marking the volume as "Secondary" may cause data loss
        - PVC should also be in deleted state, to ensure no inadvertent usage
        prior to it being marked as "Secondary"
    1. Update VR desired state for PVC as `secondary`
1. For each protected PVC
    1. Check if VR status has reached `secondary` state
        1. If not, update status as progressing
1. Report status as completed, if not progressing or error for any PVC

### Finalizing VRG when deleted

1. Check if `vrg-protection` finalizer is present on VRG instance, if not end
  reconciliation
1. Select all PVCs that match `PVCSelector` for processing
1. For each PVC
    1. Prepare and protect PVC as [above](#pvc_preparation_and_protection_for_all_states)
    1. Reconcile PVC as "Primary" or "Secondary" as its final state, based on
    the VRG state
    1. Undo PVC protections:
        1. Undo PV retention, if it was retained, based on the annotation
        `vr-retained`
        1. Remove `pvc-vr-protection` finalizer from the PVC
    1. Delete VR for the PVC
1. Report status as completed, if not progressing or error for any PVC

## Open TODOs

1. When do the objects in the S3 store get cleaned up?
    - When a VRG is deleted as Primary, it should mean the PVCs protected are
    no longer required. Deleting a VR when marked primary would also garbage
    collect the storage volume, hence this state may serve as the decision
    point to delete the backed up PV cluster data
1. How/when do we make the VRG restore from the S3 store if desired,
  when created?
    - This would be in lieu of the OCM hub taking restore actions, in a
    non-OCM world (which is at present a user expectation)
1. We need to only deal with objects that we manage (IOW, created) and not
  other user created objects
1. As we watch VR resources, we need not requeue for reconcile, rather we can
  let the the API server trickle requeue of VRG as VR status/metadata changes
    - Along the same lines, we can improve the PVC predicate filter to trigger
    VRG reconciles on phase change, annotations and finalizers
    - This would ensure we are not reconciling rapidly, but are triggered when
    respective resources change state
1. VRG state remains unimplemented
