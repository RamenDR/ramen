# Declaring ManagedCluster DR configuration

## Problem statement

Currently, any storage vendor requiring to create specific classes for storage
management, has to look at the DRPolicy at the hub cluster and automate the
required storage class CRUD (and configuration). A storage plugin at the hub, or
a plugin at every managed cluster watching related resources at the hub, becomes
a requirement as a result.

Given the above from a Ramen perspective, we would like to,

- Allow storage vendors to act on local resources on the cluster where they are
  providing storage services
- Disassociate any inter-dependency between related Ramen resources, and make
  the configuration requirement explicit for the storage vendor

## Introduce DRClusterConfig resource

Ramen would create a new cluster scoped CR based on the following CRD
specification on each ManagedCluster

```
kind: DRClusterConfig
metadata:
  Name: <cluster-name-on-ocm-hub>
spec:
  replicationSchedules:
    - <intervals>
  clusterID: <ID>
  peerClusters:
  - name: <name>
    id: <ID>
```

`spec.replicaitonSchedules` would carry a list of desired replication schedules
for various VolumeReplicationClass resources that maybe created by the
respective storage providers on the cluster.

**NOTE:** A storage vendor that does not support VolumeReplication scheme can
choose to ignore the same.

`spec.clusterID` would carry the ManagedCluster claim value for `id.k8s.io` as
its value.

`spec.peerClusters` would carry a list of peer clusters clusterID values as
described above.

The claim value for `id.k8s.io` on a,

- k8s cluster is the UID of the kube-system namespace
  `kubectl get ns kube-system -o jsonpath='{.metadata.uid}'`
- Openshift cluster is (potentially)
  `oc get clusterversion -ojsonpath='{.spec.clusterID}'`

**NOTE:** The CRD should be able to represent both a sync and/or an async
relationship that exists for this cluster.

## Implementation notes

- The Hub cluster ramen operator would create the ManagedCluster DRClusterConfig
  CRs using a ManifestWork
- The MW would be created and updated when reconciling a DRCluster
    - **NOTE:** A DRCluster would need to watch and reconcile its instance for
      any DRPolicy that is created referencing it
- The MW would be deleted when the DRCluster resource at the hub is deleted

**NOTE:** Ramen on the managed cluster will not reconcile the required resource
created, it is present for dependent storage providers or other components that
are interested in the Ramen based DR config on this cluster
