# Ramen

Ramen is a **disaster-recovery orchestrator** for stateful applications across
a set of peer kubernetes clusters, that are deployed and managed using
[open-cluster-management](https://open-cluster-management.io/concepts/architecture/)
(**OCM**), and provides **cloud-native interfaces** to orchestrate the
life-cycle of an application's state. These include:

- Failing over an application's state to a peer cluster on unavailability of
  the currently deployed cluster
- Migration of an application's state to a peer cluster

Ramen relies on storage plugins providing support for the CSI addon
[storage replication extensions](https://github.com/csi-addons/volume-replication-operator)
, of which [ceph-csi](https://github.com/ceph/ceph-csi/) is a sample
implementation.

For details regarding use-cases for Ramen see the [motivation](docs/motivation.md)
guide.

## Getting Started and Documentation

For installation, deployment, and administration, see the [install](docs/install.md)
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

Ramen is under the Apache 2.0 license.
