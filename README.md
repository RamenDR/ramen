# Ramen

Ramen is a **disaster-recovery orchestrator** for stateful applications across
a set of peer kubernetes clusters that,

- Preserve state using persistent volumes provisioned using [ceph-csi](https://github.com/ceph/ceph-csi/)
- Uses the CSI [storage replication extensions](https://github.com/csi-addons/volume-replication-operator)
  , which enable ceph-csi to replicate state across peer clusters
- Are deployed and managed using [open-cluster-management](https://open-cluster-management.io/concepts/architecture/)

Ramen provides **cloud-native interfaces** to orchestrate the life-cycle of an
applications state across peer clusters. These include,

- Failing over an applications state to a peer cluster on unavailability of
  the currently working cluster
- Migration of an applications state to a peer cluster

**NOTE:** Application deployment life cycle is managed as part of OCM
orchestrators, and Ramen concerns itself with the application state only.

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
