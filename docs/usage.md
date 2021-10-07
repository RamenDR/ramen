# Sample workload management using Ramen

## Usage

This document outlines the general life cycle/use cases for RamenDR:

  1. [Basic requirements for getting started](#Basic-Requirements)
  1. [How to protect an application](#Protecting-An-Application)
  1. [How to perform failover](#Failover)
  1. [How to perform relocate](#Relocate)
  1. [How to remove DR protection](#Delete)

The vast majority of effort is spent in the system setup and application
protection steps. Once these are ready, failover and relocate steps are
straightforward.

## Basic Requirements

  1. An environment that can run Ramen. Installation requirements are [here](install.md),
  but you'll need a hub cluster and managed cluster at minimum. Minikube clusters
  are ok.
  1. An application that a) can be deployed as a container through Kubernetes,
  b)uses persistent storage (PVCs), c) has the source code available. An example
  Busybox application can be found [here](https://github.com/RamenDR/ocm-ramen-samples/tree/main/busybox)
  with documentation [here](https://github.com/RamenDR/ocm-ramen-samples/blob/main/README.md)
  1. An S3 store where Ramen backup data can be stored. [Minio is ok](install.md)
  1. A labeling scheme to mark your applications PVCs so Ramen can find them. For
  the Busybox demo, this is `appname=busybox`
  1. A scheduling interval for how often async data should be backed up. For the
  busybox demo, this is one minute, or `1m`. Note that this value should always
  match the VolumeReplicationClass's `Spec.Parameters.SchedulingInterval` value.
  1. Text editor to create yaml files
  1. CLI to apply yaml files

### System Description

The Busybox demo example uses a two cluster structure: a hub cluster named
`hub` and two managed clusters named `east` and `west`. Note that if you've
deployed the example with [e2e scripts](../hack/ocm-minikube-ramen.sh), this
is not the default setup.

The hub cluster is where all the commands will be run. One managed clusters
("east") will run the application, and eventually the app will fail over to the
other managed cluster ("west").

Show cluster list:

`minikube profile list`

```bash
|---------|-----------|---------|---------|------|---------|---------|-------|
| Profile | VM Driver | Runtime |   IP    | Port | Version | Status  | Nodes |
|---------|-----------|---------|---------|------|---------|---------|-------|
| east    | kvm2      | docker  | x.x.x.x | 8443 | v1.20.2 | Running |     1 |
| hub     | kvm2      | docker  | x.x.x.y | 8443 | v1.20.2 | Running |     1 |
| west    | kvm2      | docker  | x.x.x.z | 8443 | v1.20.2 | Running |     1 |
|---------|-----------|---------|---------|------|---------|---------|-------|
```

Show managed clusters from hub perspective:

`kubectl get managedcluster --context=hub`

Note that the hub can also be a managed cluster, but this is unused in the demo.

```bash
NAME   HUB ACCEPTED   MANAGED CLUSTER URLS   JOINED   AVAILABLE   AGE
east   true           https://localhost      True     True        57m
hub    true           https://localhost      True     True        62m
west   true           https://localhost      True     True        52m
```

Show the data replication relationship with Ceph between the managed clusters:

`kubectl get cephcluster -n rook-ceph --context=east`

```bash
NAME        DATADIRHOSTPATH   MONCOUNT   AGE   PHASE   MESSAGE
  HEALTH      EXTERNAL
rook-ceph   /var/lib/rook     1          39m   Ready   Cluster created successfully
  HEALTH_OK
```

`kubectl get cephcluster -n rook-ceph --context=west`:

```bash
NAME        DATADIRHOSTPATH   MONCOUNT   AGE   PHASE   MESSAGE
  HEALTH      EXTERNAL
rook-ceph   /var/lib/rook     1          37m   Ready   Cluster created successfully
  HEALTH_OK
```

## Protecting an Application

### Overview

  1. Have an application you want to protect
  1. Define a Channel to the source code for that application
  1. Create a [DRPolicy](https://github.com/RamenDR/ramen/blob/main/docs/drpolicy-crd.md)
  object to describe the data replication relationship for the cluster that application
  will run on
  1. Label the PVCs in use by that application so that Ramen can find them (this
  will match a DRPlacementControl object later)
  1. Define the PlacementRule for defining where an app is deployed, its labels,
  and add the Ramen scheduler to override OCM's default Placement Reconciller.
  1. Subscribe to the Pod and PVC from the Channel that was created.
  1. Define the [DRPC](https://github.com/RamenDR/ramen/blob/main/docs/drpc-crd.md)
  object that ties the DRPolicy, PlacementRef and PVCSelector labels together.
  1. Apply the yaml files defined above.
  1. Wait for pods to start running on preferred cluster.

### Details

  1) Have an application defined somewhere in GitHub, Helm or S3 storage. This
  walkthrough will use GitHub as the example source of a Busybox application.
  Details on the sample application can be found [here](https://github.com/RamenDR/ocm-ramen-samples/tree/main/busybox).
  2) Create Channel that describes where the application source resides.
  Sample file is available [here](https://github.com/RamenDR/ocm-ramen-samples/blob/main/subscriptions/channel.yaml).

```yaml
---
apiVersion: apps.open-cluster-management.io/v1
kind: Channel
metadata:
  name: ramen-gitops
  namespace: ramen-samples
spec:
  type: GitHub  # define channel type
  pathname: https://github.com/RamenDR/ocm-ramen-samples.git  # same as clone path
```

Apply and verify with:

`$ kubectl get channel -n ramen-samples ramen-gitops`

```bash
NAME           TYPE     PATHNAME                                           AGE
ramen-gitops   GitHub   https://github.com/RamenDR/ocm-ramen-samples.git   70s
```

  3) Create DRPolicy object that describes which clusters are participating in
  the data replication relationship. This will also define the schedulingInterval
  and s3ProfileName.
  Example [here](https://github.com/RamenDR/ocm-ramen-samples/blob/main/subscriptions/drpolicy.yaml).
  **Important**: `schedulingInterval` MUST match the value of the VolumeReplicationClass's
  `schedulingInterval` value.

```yaml
---
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPolicy
metadata:
  name: dr-policy
spec:
  drClusterSet:
  - name: east  # primary cluster
    s3ProfileName: minio-on-hub  # S3 store for backup data on primary
  - name: west  # cluster that replicates data from primary cluster
    s3ProfileName: minio-on-hub  # S3 store for backup data on secondary
  schedulingInterval: 1m  # how often async data is replicated between clusters
```

Apply and verify with:

`kubectl get drpolicy -n ramen-samples dr-policy -o jsonpath='{.spec}{"\n"}'`

```bash
{"drClusterSet":[{"name":"east","s3ProfileName":"minio-on-hub"},{"name":"west","s3ProfileName":"minio-on-hub"}],"schedulingInterval":"1m"}
```

  4) Add a label to your PVCs so that Ramen can find them. In this example, the
  label `appname=busybox` is used. Sample PVC yaml [here](https://github.com/RamenDR/ocm-ramen-samples/blob/main/busybox/busybox-pvc.yaml)

```yaml
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: busybox-pvc
  labels:
    appname: busybox  # label to use with protected app
spec:
  accessModes: [ "ReadWriteOnce" ]
  storageClassName: "rook-ceph-block"
  resources:
    requests:
      storage: 1Gi
```

Apply this file.

  5) Define the PlacementRule, which defines where an app can be deployed,
  which labels are applied to the app when it's deployed, and set up Ramen as
  the placement scheduler. `ClusterReplicas` is the number of clusters that an
  application should be placed on. However, this should always be `1` for an
  application targeted for DR.
  Example [here](https://github.com/RamenDR/ocm-ramen-samples/blob/main/subscriptions/busybox/placementrule.yaml).

```yaml
---
apiVersion: apps.open-cluster-management.io/v1
kind: PlacementRule
metadata:
  name: busybox-placement
  namespace: ramen-samples
  labels:
    app: busybox-sample  # define labels applied to app here
spec:
  clusterConditions:  # define cluster deployment strategy
    - type: ManagedClusterConditionAvailable
      status: "True"
  clusterReplicas: 1  # should always be 1
  schedulerName: "ramen"  # define placement scheduler (will always be "ramen")
```

Apply this file.

  6) Set up a Subscription, which uses the labels, Channel and PlacementRule
  created in previous steps. Example [here](https://github.com/RamenDR/ocm-ramen-samples/blob/main/subscriptions/busybox/subscription.yaml).
  Note that the annotations define the Github branch (main) and path (busybox
  directory).

```yaml
---
apiVersion: apps.open-cluster-management.io/v1
kind: Subscription
metadata:
  annotations:  # define branch and path for source
    apps.open-cluster-management.io/github-branch: main
    apps.open-cluster-management.io/github-path: busybox
  labels:
    app: busybox-sample  # notice label re-use here
  name: busybox-sub
  namespace: ramen-samples
spec:
    channel: ramen-samples/ramen-gitops  # reference resource name of channel here
    placement:
      placementRef:
        kind: PlacementRule
        name: busybox-placement  # reference PlacementRule here
```

Apply this file.

  7) The DRPlacementControl object defines how a PlacementRule should be handled
  by Ramen. Example [here](https://github.com/RamenDR/ocm-ramen-samples/blob/main/subscriptions/busybox/drpc.yaml).
  Key components: the PlacementRef, which defines the Placement rule to use
  (notice this references the PlacementRule defined in the last step). The
  drPolicyRef references the [DRPolicy](https://github.com/RamenDR/ramen/blob/main/docs/drpolicy-crd.md).
  PreferredCluster, which defines the first choice cluster where the app should
  be deployed, if it's available. PVCSelector: defines which labels are needed
  for protection on PVCs in the given namespace (this is the label attached to
  the PVCs also).

```yaml
---
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: busybox-drpc
  labels:
    app: busybox-sample
spec:
  preferredCluster: "east"  # where Ramen will try to place an app by default
  drPolicyRef:
    name: dr-policy  # reference DRPolicy object defined earlier
  placementRef:
    kind: PlacementRule
    name: busybox-placement  # reference PlacementRule defined earlier
  pvcSelector:
    matchLabels:
      appname: busybox  # reference labels defined earlier
```

Apply this file.

  8) Now all the files are ready for deployment. Apply yaml files for Namespace,
  PlacementRule, Subscription and DRPC. Example file list [here](https://github.com/RamenDR/ocm-ramen-samples/blob/main/subscriptions/busybox/kustomization.yaml)
  `$ kubectl --context=hub apply -k ocm-ramen-samples/subscriptions/busybox`
  9) Wait for pods to start running on preferred cluster:
  `$ watch kubectl get pods -n busybox-sample --context=east`. Once the pod is
  Running, the application is protected and data can be written to the PVCs.
  10) Check that the PlacementRule has selected the correct cluster to place the
  app by running:
  `kubectl get placementrule busybox-placement -n busybox-sample -o yaml | less`
  and looking at the `Status.Decisions` field: for our example, `clusterName=east`
  and `clusterNamespace=east`

### Scribbling data

For testing purposes, scribble data however you'd like. Here's an example:

```bash
# run busybox container shell
kubectl exec -it -n busybox-sample busybox --context=east -- sh

# from container (should see / # prompt)
# write some data to mount point defined in Pod spec: mountPath: /mnt/test
echo "east" > /mnt/test/a.txt

# run sync so the disk saves it
sync

# verify data is present
cat /mnt/test/a.txt

# wait the amount of time defined by DRPolicy.Spec.SchedulingInterval
```

Verify that the PVC is present in the S3 store:

  1. Get PVC name: `kubectl get pvc -n busybox-sample --context=east` and look
  at the Volume name.
  1. Check that backing store has PVC backed up in the S3 store (configured in
  DRPolicy): `./mc ls mycloud/busybox-sample-busybox-drpc/v1.PersistentVolume`
  this should show the same Volume name as step 1.

## Failover

Update existing DRPlacementControl with "Failover" action and specify the
cluster to failover to. Note the diff between the DRPC in the example and the
version [here](https://github.com/RamenDR/ocm-ramen-samples/blob/main/subscriptions/busybox/drpc-failover.yaml),
as highlighted below:

```yaml
spec:
  action: "Failover"
  failoverCluster: "west"
```

What should happen next:

  1. If the preferred cluster is running, the app pods will be terminated. Even
  though this step is shown first, there is no waiting on this to occur - step 2
  effectively happens in parallel with step 1.
  1. The failover cluster will create pods for the app that was running on the
  preferred cluster. Wait until the pods have Status=Running.
  1. Data should now be available on the failover cluster.

Verifying example data is on failover cluster:

  1. Run shell in our application's pod:
  `kubectl exec -it n busybox-sample busybox --context=west -- sh`
  1. Verify our data is present: `/ # cat /mnt/test/a.txt` should read "east" but
  runs on "west" cluster.
  1. Verify the PVC volumes are present:
  `kubectl get pvc -n busybox-sample --context=west` should output the same PVC volume
  name as seen on the east cluster and in the S3 store.

## Relocate

Note on failover vs relocate: failover may include data loss due to asynchronous
backup. Relocate is a clean handoff between clusters and doesn't require the sync
period or data loss in the process.

High level overview: application pods are running on west cluster; write some data
to west cluster but don't wait for sync interval, initiate the relocate action
with DRPlacementControl spec, then verify the pod and data are available on the
east cluster and not the west cluster.

How to do it:
Update DRPlacementControl to use `Spec.Action=Relocate`, which will initiate the
Relocate action. Note that the cluster isn't specified; Ramen will attempt to relocate
to the PreferredCluster as defined in the DRPlacementControl spec (in our running
example, this is "east").

Demonstration:

  1. Write some data to the west cluster:
  `kubectl exec -it -n busybox-sample busybox --context=west -- sh` then
  `echo "relocate" > /mnt/test/a.txt`
  (notice this will rewrite existing "east" with "relocate")
  1. Edit or apply the DRPControl object to use action=Relocate and ensure preferredCluster
  is "east"

What should happen:

  1. Pods running on West cluster should sync to storage terminate - this is
  to ensure there's no data loss. Proceed to step 2 once West pods are fully terminated.
  1. Pods should be created on East cluster and run.
  1. Data from West cluster should be available on the East cluster.

## Delete

This isn't supported yet.

Issue #313 tracks adding this use case.