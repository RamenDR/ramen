# Volume Replication Group (VRG) usage

## Protect application on cluster1

1. Deploy **cluster1** VRG with `Spec.ReplicationState: primary` in
 application's namespace
1. Wait for **cluster1** VRG `Status.Conditions.ClusterDataProtected: true`
 indicating application's Kube objects have been protected

## Unprotect application

1. Delete VRG with `Spec.ReplicationState: primary` to delete its Kube object
 replicas or `Spec.ReplicationState: secondary` to preserve them

## Failover application from cluster1 to cluster2

1. Fence **cluster1** and **cluster2** VRGs from each other's Kube object
 replicas
   - This is typically accomplished by network fencing
1. Fence **cluster1** and **cluster2** VRGs from each other's volume data
   - For `Async` mode, this is typically accomplished by setting **cluster2**
 VRG `Spec.ReplicationState: primary`
   - For `Sync` mode, this is typically accomplished by network fencing
1. Deploy **cluster2** VRG with `Spec.ReplicationState: primary` in the
 `Namespace` it was in and with the `Name` it had on **cluster1**
   - Kube objects are recovered from the first available replica store specified
 in VRG's `Spec.S3Profiles` containing a replica for its `Namespace` and `Name`
1. Wait for **cluster2** VRG `Status.Condtions`:
   - `ClusterDataReady: true` indicating its Kube objects have been recovered
   - `DataReady: true` indicating its volumes have been recovered
1. **cluster2** application protection resumes automatically

## Failback/Relocate application from cluster2 to cluster1

1. Set **cluster1** VRG `Spec.ReplicationState: secondary`
1. Undeploy **cluster1** application
   - Its PVCs can finally be deleted once VRG is deleted in a subsequent step
   - This allows its Kube objects to be recovered in a subsequent step
1. Delete **cluster1** VRG
   - This allows its PVCs to finally be deleted
1. Unfence **cluster1** and **cluster2** VRGs from each other's Kube object
 replicas
   - This is typically accomplished by network unfencing
1. Unfence **cluster1** and **cluster2** VRGs from each other's volume data
   - For `Async` mode, this is typically accomplished by setting **cluster1**
 VRG `Spec.ReplicationState: secondary`
   - For `Sync` mode, this is typically accomplished by network unfencing
1. Wait for **cluster2** VRG `Status.Conditions.ClusterDataProtected: true`
1. Set **cluster2** VRG `Spec.ReplicationState: secondary`
1. Undeploy **cluster2** application
   - Its PVCs can finally be deleted once VRG is deleted in a subsequent step
1. For `Async` mode, synchronize volume data:
   1. Deploy **cluster1** VRG with `Spec.ReplicationState: secondary`
   1. Wait for **cluster1** VRG `Status.Conditions.DataReady: true` indicating its
 volume data are in-sync with **cluster2**'s
1. Delete **cluster2** VRG
   - This allows its PVCs to finally be deleted
1. Set, for `Async` mode, or deploy, for `Sync` mode, **cluster1** VRG
 `Spec.ReplicationState: primary` to recover its volumes and Kube objects
1. Wait for **cluster1** VRG `Status.Condtions`:
   - `ClusterDataReady: true` indicating its Kube objects have been recovered
   - `DataReady: true` indicating its volumes have been recovered
1. **cluster1** application protection resumes automatically
