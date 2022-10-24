# ManagedClusterView

## Usage Description

ManagedClusterViews are created on a hub cluster and return information about
a resource on a managed cluster.

Examples for use are in this directory.

## Requirements

* [multicloud-operators-foundation](https://github.com/
  open-cluster-management/multicloud-operators-foundation) needs to be
  installed in order to make this resource work.
* System in use contains a hub cluster and managed cluster

## How to use

The example below shows how to create a ManagedClusterView on a hub cluster.
The MCV looks for a VolumeReplicationGroup named
'volumereplicationgroup-sample-1' on managedcluster 'cluster1'.
The comments are to give context for the other ways to specify a resource,
along with required/optional components:

```yaml
apiVersion: view.open-cluster-management.io/v1beta1
kind: ManagedClusterView
metadata:
  name: example-vrg-mcv
  namespace: cluster1  # managed cluster name. Namespace should exist on hub
spec:
  scope:
    name: volumereplicationgroup-sample-1  # REQUIRED: name of resource to look for
    namespace: default  # OPTIONAL: namespace of resource on managedcluster

    # two ways to define resource to query. Only use one.
    # 1) resource
    resource: VolumeReplicationGroup

    # 2) apiGroup + Kind + Version
    #apiGroup: apps/v1
    #kind: PersistentVolume
    #version: v1

    # misc parameters
    # updateIntervalSeconds: 1  # OPTIONAL: update interval for resource query
```

For a local setup with minikube named 'hub' and managed cluster named
'cluster1', perform the following steps to verify everything works:

```bash
# ensure ManagedClusterView is available
kubectl get crd | grep managedclusterview

# make sure resource exists on managed cluster
kubectl get volumereplicationgroup volumereplicationgroup-sample-1 \
  --namespace=default --context=cluster1

# create ManagedClusterView to query that resource from hub
kubectl config use-context hub
kubectl apply -f ./mcv-vrg.yaml

# verify contents
kubectl get managedclusterview example-vrg-mcv --namespace=cluster1 -o yaml
```

If everything worked, in the Status field, you should see something like this:

```yaml
status:
  conditions:
  - lastTransitionTime: "2021-06-15T21:05:11Z"
    message: Watching resources successfully
    reason: GetResourceProcessing
    status: "True"
    type: Processing
  result:
    apiVersion: ramendr.openshift.io/v1alpha1
    kind: VolumeReplicationGroup
    metadata:
    # metadata omitted for example
```

The important parts are in Status.Condition. The 'status' field of the most
recent status should be "True" with type "Processing"; this will show when
ManagedClusterView is functioning correctly.
