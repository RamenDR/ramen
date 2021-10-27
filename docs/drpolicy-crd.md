# DRPolicy

A disaster recovery (DR) policy resides in an Open Cluster Management (OCM) hub cluster.
A hub may have one or more DR policies.
A DR policy is a cluster, not namespace, scoped resource with the following inputs:

- A frequency to snapshot each persistent volume (PV) for replication
- Names of clusters that it applies to
- Name of each cluster's s3 object store profile to retrieve cluster data from

A cluster may be specified in more than one DR policy.

## `spec.schedulingInterval`

PV snapshot frequency is expressed as a decimal number followed by a time unit
abbreviation of `m` from minutes, `h` for hours, or `d` for days.
Each cluster specified must have a `VolumeReplicationClass` instance
specifying the same snapshot frequency.

## `spec.drClusterSet[]`

- `name`: Kubernetes cluster name
- `s3ProfileName`: Name of s3 store profile defined in cluster's `RamenConfig.s3StoreProfiles[]`

## `status.conditions[]`

- `type: Validated`: the specified s3 profiles have passed a connectivity test

## Example

Here is an example DR policy:

```sh
$ kubectl --context hub get drpolicy/dr-policy -oyaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPolicy
metadata:
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: |
      {"apiVersion":"ramendr.openshift.io/v1alpha1","kind":"DRPolicy","metadata":{"annotations":{},"name":"dr-policy"},"spec":{"drClusterSet":[{"name":"cluster1","s3ProfileName":"minio-on-hub"},{"name":"cluster2","s3ProfileName":"minio-on-hub"}],"schedulingInterval":"1m"}}
  creationTimestamp: "2021-10-27T01:42:23Z"
  finalizers:
  - drpolicies.ramendr.openshift.io/ramen
  generation: 2
  name: dr-policy
  resourceVersion: "19626"
  uid: 6d5a3a12-ca34-4b0e-8dba-a319e120320b
spec:
  drClusterSet:
  - name: cluster1
    s3ProfileName: minio-on-hub
  - name: cluster2
    s3ProfileName: minio-on-hub
  replicationClassSelector: {}
  schedulingInterval: 1m
status:
  conditions:
  - lastTransitionTime: "2021-10-27T01:42:23Z"
    message: drpolicy validated
    observedGeneration: 1
    reason: Succeeded
    status: "True"
    type: Validated
```
