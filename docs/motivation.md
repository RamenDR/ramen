# Motivation

## The problem

Consider a Kubernetes cluster that has an application deployment, such as a
MySQL database, which uses a PersistentVolume (PV) to store its data.  Such
applications are also referred to as stateful applications or
[StatefulSets](https://kubernetes.io/docs/tutorials/stateful-application/basic-stateful-set/).

A disaster event, be it a natural disaster, such as an earthquake, power failure
or flooding, etc., or a man-made disaster, such as misconfigurations, malice,
etc., could leave a Kubernetes cluster unusable.

Until the problems afflicting the cluster are fixed, the affected applications
may be unstable or plain unusable, resulting in a loss of system availability,
aka, unscheduled downtime and potential loss of revenue. Businesses that offer
critical services to its users aim to reduce such unscheduled application
downtime by preparing a business continuity plan, which includes a disaster
prevention plan and a disaster recovery (DR) plan.

Note that restarting the application in a different recovery cluster that is not
prepared for a DR event, may result in an undesirable empty database due to
dynamic provisioning of a new PV.

## The Ramen DR Solution

The Ramen project is an open source DR orchestration software that has the
following features:

1. Lower RTO: Ramen integrates with the storage replication layer to offer a DR
   solution that replicates the PV data to a peer cluster.  Compare this to
   backup-based DR solutions that require moving large volumes of data from an
   object store to the recovery cluster resulting in a higher RTO.
1. Application DR: Ramen integrates with
   [OCM](https://open-cluster-management.io/) to offer a DR solution that also
   recovers the affected applications, not just PVs. Compare this to DR
   solutions that merely protect the data volumes and leave it upto the user to
   recover the applications.

## DR Background

Recovering from a disaster involves starting the application, any dependent
services along with the application data on another cluster. The amount of
downtime a business can tolerate depends on the potential loss of revenue, which
in turn affects their choice of the disaster recovery solution. Businesses
understand the tradeoff between the cost of unscheduled downtime vs. the cost of
the disaster recovery solution. Businesses use a variety of DR solutions
depending on their needs.

### High Availability (HA) vs Disaster Recovery (DR)

Kubernetes has many HA features built into it:

- Separating the control plane from the worker nodes.
- Replicating the control plane components on multiple nodes.
- Load balancing traffic to the cluster's API server.
- Having enough worker nodes available, or able to quickly become available, as
  changing workloads warrant it.

HA features address sub-component failure. Whereas, DR is recovering
applications that were running on a failed cluster.  A cluster may fail when its
subcomponents fail beyond the HA limits.  A cluster may be considered to be
failed when it becomes inaccessible to its clients, say due to a network
failure.

Kubernetes does not have DR built into it.

### Cluster Data vs Volume Data

To protect and recover a Kubernetes cluster, the following have to be protected:

1. The cluster data in etcd: this includes the deployments, services, pods,
   containers, PVCs, PV, etc., resources.
1. The volume data contents of PVs.

### DR Clustering Models

#### Stretched Cluster

A single Kubernetes cluster is stretched across data centers. In this model, the
solution uses the HA features of Kubernetes (node failure tolerance) to deliver
DR, but an update to the etcd database is synchronously written across the data
centers, which increases client operation latencies compared to a single
unstretched Kubenetes cluster that is located within a data center.  Reducing
the radius of such a stretched cluster will reduce the latency but may not meet
the needs of physical separation that some businesses require to protect
themselves from certain disasters like earthquake, flooding, power-failure, etc.

#### Metro DR

One or more peer recovery clusters are located in another data center or
availability zone within the Metro area or the same region. In this model, each
of the peer Kubernetes clusters have their own etcd database (different from the
stretched cluster solution but similar to the Regional DR solution).

#### Regional DR

One or more peer recovery clusters are located in different regions that are
more than 300 km away. In this model, each of the peer Kubernetes clusters have
their own etcd database (different from the stretched cluster solution but
similar to the Metro DR solution).

### DR Protection Models

#### Backup based DR

The cluster data and volume data are stored in a secondary storage, say in an
object store.  On a DR event, the data and applications are downloaded from the
object store to the recovery cluster.

#### Replication based DR

The  cluster data and volume data is replicated to a peer cluster. The
replicated content may be published to the user (become visible) only after a DR
event. Comparing this to the backup based DR, the replicated-based DR doesn't
need to download the volume data from, say an object store, to the recovering
cluster.
