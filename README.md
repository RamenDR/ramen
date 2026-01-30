<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Ramen

Ramen is an
[open-cluster-management (OCM)](https://open-cluster-management.io/docs/concepts/architecture/)
[placement](https://open-cluster-management.io/docs/concepts/content-placement/placement/)
extension that provides **Kubernetes-native Disaster Recovery** for
[workloads](https://kubernetes.io/docs/concepts/workloads/) and their
persistent data across a pair of OCM managed clusters. Ramen orchestrates,
workload protection and placement on managed clusters through:

- **Relocate**: Planned migration to a peer cluster for maintenance,
  optimization, or failback
- **Failover**: Unplanned recovery to a peer cluster after cluster loss or
  failure

## Persistent Data Protection

Ramen supports several approaches for replicating persistent data across
clusters:

### Storage Vendor Assisted Replication

Ramen uses storage plugins that implement the CSI
[storage replication specification](https://github.com/csi-addons/spec/tree/main/replication)
and csi-addons `Volume[Group]Replication`
[APIs](https://github.com/csi-addons/kubernetes-csi-addons/tree/main/api/replication.storage/v1alpha1)
to orchestrate volume replication and recovery across clusters.

[Ceph-csi](https://github.com/ceph/ceph-csi/) is one such plugin that
implements the csi-addons APIs.

### Volsync Based Replication

For storage that supports Kubernetes `Volume[Group]Snapshots` APIs, Ramen uses
the [volsync](https://volsync.readthedocs.io/en/stable/) rsync plugin to
transfer periodic snapshots to peer clusters.

### Highly Available Storage Backends

When storage is already highly available and requires no replication, Ramen
manages only workload resources. It ensures access guarantees through the
csi-addon
[fencing specification](https://github.com/csi-addons/spec/tree/main/fence)
and and csi-addons
[NetworkFence](https://github.com/csi-addons/kubernetes-csi-addons/blob/main/docs/networkfence.md)
APIs.

**NOTE:** This differs from synchronously replicated storage systems, which
still require replication state management and therefore would use [Storage Vendor
Assisted Replication](#storage-vendor-assisted-replication).

## Workload Resource Protection

Ramen supports several approaches for protecting and replicating application
resources across clusters:

### GitOps Based Protection (Recommended)

Applications deployed via **GitOps** (e.g., ArgoCD ApplicationSets) with
OCM-[managed](https://open-cluster-management.io/docs/scenarios/integration-with-argocd/)
Placements work directly with Ramen's placement orchestration. This is the
most common deployment pattern for cloud-native applications.

### Discovered Applications

Ramen can also protect applications deployed through other methods (e.g.,
kubectl, kustomize) by using [velero](https://velero.io/docs/main/) to
automatically identify and back up application resources.

### Recipe-based Protection

[Recipes](docs/recipe.md) define vendor-supplied workflows for capturing and
recovering complex stateful applications. They specify:

- Application-specific capture workflows
- Recovery workflows with proper sequencing
- Custom hooks for pre/post actions

Recipes extend [discovered applications](#discovered-applications) protection
by enabling multi-step workflows beyond simple backup and restore.

**Target Audience:**

- **Software Vendors**: Ship recipes with their products for out-of-the-box
  protection
- **Advanced Users**: Write custom recipes for applications without vendor
  support

## Getting Started

- [Installation guide](docs/install.md)
- [Configuration guide](docs/configure.md)
- [Usage guide](docs/usage.md)

## Contributing

We welcome contributions. See the [contributing guide](CONTRIBUTING.md) to get
started, or open an [issue](https://github.com/ramendr/ramen/issues) to report
bugs, suggest improvements, or request features.

## Project Status

Ramen is under active development. All APIs are currently **alpha** with no
stable releases yet.

- **Alpha**: APIs may change incompatibly between releases. Recommended only
  for short-lived testing clusters due to potential bugs and lack of long-term
  support.

## Licensing

Ramen is under the [Apache 2.0 license.](LICENSES/Apache-2.0.txt)
