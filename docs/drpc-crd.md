# DRPlacementControl(drpc) CRD

DRPlacementControl (DRPC) is a Kubernetes operator that runs in the ACM Hub cluster. Its main function is to manage the lifecycle of stateful applications. It implements the reconciliation loop pattern. The reconciliation process is triggered when a DRPC CR is created, updated, or deleted.

An application that requires DR protection creates a DRPC CR. The CR specifies references to a DRPolicy, a PlacementRule, and others that we'll talk about later below.

Each instance of the DRPC uses a DRPolicy to obatain the managed clusters that form a peering relationship for the DR replication. This set of managed clusers are used for initial deployment and failover/relocate actions.

The PlacementRule that the DRPC points to is the one that is included as part of the ACM resouces to be created for the application. We call that PlacementRule a `User PlacementRule`. The user sets **ramen** as the `schedulerName` for the PlacementRule.  When that is set, the PlacementRule will not run its algorithm to make a placement decision, but instead, it will delegate that work to the DRPC instance. Once the DRPC decides where to place the application, it updates the PlacementRule with that decision and ACM deploys the application to the designated managed cluster.

# Creating DRPCs
To get started, here is a simple example of a CRD to deploy a DRPC
```
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: my-busybox-drpc
  namespace: busybox-sample
  labels:
    app: busybox-sample
spec:
  drPolicyRef:
    name: dr-policy-name
  placementRef:
    kind: PlacementRule
    name: my-busybox-placement
  pvcSelector:
    matchLabels:
      appname: busybox
```

# Settings
If any settings are not specified, a suitable default will be used automatically.

# DRPC Metadata
* `name`: The name of the DRPC instance
* `namespace`: The namespace of the DRPC instance, which is also the namespace of the application ACM components (Subscription, PlacementRule, Channel, ..., etc.) for the DR targeted application`

# DRPC Spec
* `PlacementRef`: A required field. It references to the user PlacementRule for the application that is targeted for the DR protection
* `DRPolicyRef`: A required field. It references the DRPolicy used by this instance of DRPC
* `PVCSelector`: A required field. It is a label selector for this DRPC instance, which is passed to the VolumeReplicationGroup and used to identify all the PVCs that need DR protection.
* `PreferredCluster`: This is the cluster name that the user prefers to run the application on. Its value MUST be one of the clusters in the referenced DRPolicy. If left empty, the workload is dynamically selected to be placed on one of the clusters in the referenced DRPolicy
* `Action`: Is set to either "Relocate" or "Failover". This is not a required field on initial deployment, however, it is a required field for either relocation or failover. If action is set to "Relocate", the workload is relocated to the preferredCluster ONLY if both, the current cluster hosting the workload and the preferredCluster are available. A "Relocate" action incurs no data loss, but does incur a loss of workload availability, which is the duration it takes for the workload to be relocated. If action is set to "Relocate" and preferredCluster is not specified, the workload remains on the last scheduled cluster. If action is failover the workload is recovered on the failoverCluster ONLY if the failoverCluster is available. A "failover" action incurs data loss, as it does not wait for the workload to stop and finish replicating its data on the current cluster.
* `FailoverCluster`:  This is a required field when Action is set to "Failover", option otherwise. It is the cluster name that the user wants the workload to failover to when action is "Failover". Its value MUST be one of the clusters in the referenced DRPolicy. If left empty and action is set to "Failover" workload remains on the cluster where it was last scheduled to

# DRPC Status
DRPC provides status information on the state of the action via the `.status` field in the DRPC object:
```
status:
  conditions:
  - lastTransitionTime: "2021-10-12T15:59:18Z"
    message: Condition type Available
    observedGeneration: 1
    reason: Deployed
    status: "True"
    type: Available
  - lastTransitionTime: "2021-10-12T15:59:18Z"
    message: Condition type PeerReady
    observedGeneration: 1
    reason: Success
    status: "True"
    type: PeerReady
  lastUpdateTime: "2021-10-12T16:09:18Z"
  phase: Deployed
  preferredDecision:
    clusterName: cluster1
    clusterNamespace: cluster1
  resourceConditions:
    conditions:
    - lastTransitionTime: "2021-10-12T15:59:27Z"
      message: PVCs in the VolumeReplicationGroup are ready for use
      observedGeneration: 1
      reason: Ready
      status: "True"
      type: DataReady
    - lastTransitionTime: "2021-10-12T15:59:25Z"
      message: Restoring PV cluster data
      observedGeneration: 1
      reason: Restored
      status: "True"
      type: ClusterDataReady
    - lastTransitionTime: "2021-10-12T15:59:27Z"
      message: Cluster data of all PVs are protected
      observedGeneration: 1
      reason: Uploaded
      status: "True"
      type: ClusterDataProtected
    resourceMeta:
      kind: VolumeReplicationGroup
      name: busybox-drpc
      namespace: busybox-sample
```
# Conditions
In the above example, the status contains two condisions: Available and PeerReady

## Condition 'Available'
`Available` condition denotes if the workload is available in the desired state on the desired cluster. The following various Condition transitions show when the workload is transitioning to the desired state and when it has reached the desired state.
```
Conditions:
  Type            Status  Reason
  ----            ------  ------
  Available       False   Deploying, FailingOver, Relocating
  Available       True    Deployed, FailedOver, Relocated
```

## Condition 'PeerReady'

`PeerReady` condition denotes if peer clusters are available to relocate to or failover to. Possible Condition reasons are shown next.
`ReasonProgressing` indicates that the DRPC is in the process in initiating the clean-up task.
`ReasonCleaning` indicates that the process has started to clean-up the old primary. Cleaning up the old primary means ensuring that the [VolumeReplicationGroup](https://github.com/RamenDR/ramen/blob/main/docs/vrg-crd.md) (VRG) has transitioned successfully to secondary and deleted.
`ReasonSuccess` indicates that the clean up was a success
`ReasonNotStarted` indicates that the process for clean-up has not been started yet.

# LastUpdateTime
the time when status was last updated

# Phase
Denotes phase of reconciliation. Possible values are:

**Deploying**: This denotes that the initial deployment has started. During this phase, the DRPC operator creates VRG ManifestWork and updates the PlacementRule `.status.decisions` with the preferred cluster that will host the workloads.

**Deployed**: This denotes that the initial deployment is complete.

**FailingOver**: In this phase, the user has decided to failover. the DRPC operator will ensure that PVs cluster-data have been restored in the failover cluster. It creates a VRG ManifestWork targeted to the FailoverCluster, and waits for VRG operator in the target cluster to report that the PVs cluster-data have been restored before it updates the PlacementRule `.status.decisions` with the failover cluster that will host the workloads. Finally, it cleans up the preferred cluster when it is back online.

**FailedOver**: This denotes that the failover operation is complete

**Relocating**: This phase denotes that the relocation has started. The user can choose to relocate to a different cluster from the one that currently is hosting the workloads. During relocation, the DRPC operator first ensures that the current home cluster transitions successfuly to secondary by changing the VRG `.spec.replicationState` to `Secondary` (done through the existing VRG ManifestWork) and waits for a comfirmation that the VRG object has transitioned to `Secondary`. Next, is creating the VRG ManifestWork targetted for the new home cluster, waits for the PVs cluster-data to be applied, and updates the PlacementRule `.status.decisions` to the new target cluster. Finally, the peer cluster is cleaned up asynchronosely.

**Relocated**: Denotes that the relocation operation is complete

# PreferredCluster
Denotes a `PlacementDecision`, which is a tuple of `clusterName` and `clusterNamespace`, where the workload was deployed to. The value will always be the configure `preferredCluster`.  If `preferredCluster` is not configured, then this value holds the cluster that was dynamically selected to place the workload on.

# ResourceConditions

This field holds the VRG status conditions information from the cluster where the workload is deployed on. It is mainily here for information only regarding VolumeReplicationGroup resource and more fine-grained VolumeReplication resource status per PVC that is protected as part of the `.spec.pvdSelector`.



