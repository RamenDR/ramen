# Ramen

Ramen is an [open-cluster-management (OCM)](https://open-cluster-management.io/concepts/architecture/)
[placement](https://open-cluster-management.io/concepts/placement/) extension
that provides recovery and relocation services for
[workloads](https://kubernetes.io/docs/concepts/workloads/), and their
persistent data, across a set of OCM managed clusters. Ramen provides
cloud-native interfaces to orchestrate the placement of workloads and their
data on PersistentVolumes, which include:

- Relocating workloads to a peer cluster, for planned migrations across clusters
- Recovering workloads to a peer cluster, due to unplanned loss of a cluster

Ramen relies on storage plugins providing support for the CSI
[storage replication addon](https://github.com/csi-addons/volume-replication-operator),
of which [ceph-csi](https://github.com/ceph/ceph-csi/) is a sample implementation.

For details regarding use-cases for Ramen see the [motivation](docs/motivation.md)
guide.

## Getting Started and Documentation

For installation, see the [install](docs/install.md) guide.

For configuration, see the [configure](docs/configure.md) guide.

For usage of Ramen to orchestrate placement of workloads, see the [usage](docs/usage.md)
guide.

[Volume Replication Group (VRG) usage](docs/vrg-usage.md)

[Kubernetes Resource Protection](docs/krp.md)

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

Ramen is under the [Apache 2.0 license.](LICENSE)
