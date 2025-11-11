<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Ramen

Ramen is an [open-cluster-management (OCM)](https://open-cluster-management.io/concepts/architecture/)
[placement](https://open-cluster-management.io/concepts/placement/) extension
that provides **OpenShift-native Disaster Recovery** capabilities for
[workloads](https://kubernetes.io/docs/concepts/workloads/) and their
persistent data across a set of OCM managed clusters. Ramen provides
cloud-native interfaces to orchestrate the placement of workloads and their
data on PersistentVolumes, which include:

- **Relocate**: Planned migration of workloads to a
  peer cluster for maintenance or optimization
- **Failover**: Unplanned recovery of workloads to
  a peer cluster due to cluster loss or failure
- **Failback**: Restoring workloads to the
  original cluster after recovery from a disaster

Ramen relies on storage plugins providing support for the CSI
[storage replication addon](https://github.com/csi-addons/volume-replication-operator),
of which [ceph-csi](https://github.com/ceph/ceph-csi/) is a sample implementation.

## Workload Protection Methods

Ramen supports multiple approaches to protect and replicate applications across clusters:

### GitOps-based Protection (Recommended)

Applications deployed via **GitOps** (e.g., ArgoCD ApplicationSets)
are automatically protected by replicating their Git-based configuration.
This is the most common deployment pattern for
cloud-native applications.

### Discovered Applications

Ramen can discover and protect existing applications deployed
through traditional methods (e.g., kubectl, Helm).
The system automatically identifies application resources
and their persistent volumes for protection.

### Recipe-based Protection

[Recipes](https://github.com/ramendr/recipe) provide
vendor-supplied disaster recovery
specifications for complex stateful
applications. Recipes define:

- Application-specific capture workflows (e.g., database quiesce operations)
- Recovery workflows with proper sequencing
- Custom hooks for pre/post DR operations

**Target Audience:**

- **Software Vendors**: Create recipes to
  accompany their products, ensuring proper
  DR protection out-of-the-box
- **Advanced Users**: Write custom
  recipes when vendors haven't provided
  them for specific applications

Recipes complement volume replication by
capturing application-specific state and
metadata that goes beyond simple PVC
snapshots.

For details regarding use-cases for Ramen see the [motivation](docs/motivation.md)
guide.

## Getting Started and Documentation

For installation, see the [install](docs/install.md) guide.

For configuration, see the [configure](docs/configure.md) guide.

For usage of Ramen to orchestrate placement of workloads, see the [usage](docs/usage.md)
guide.

## Contributing

We welcome contributions. See [contributing](CONTRIBUTING.md) guide to get
started.

## Report a Bug

For filing bugs, suggesting improvements, or requesting new features, please
open an [issue](https://github.com/ramendr/ramen/issues).

## Project Status

The entire project is under development, and hence all APIs supported by Ramen
are currently **alpha**. There are no releases as yet.

- **Alpha:** The API may change in incompatible ways in a later software
  release without notice, recommended for use only in short-lived testing
  clusters, due to increased risk of bugs and lack of long-term support.

## Licensing

Ramen is under the [Apache 2.0 license.](LICENSES/Apache-2.0.txt)
