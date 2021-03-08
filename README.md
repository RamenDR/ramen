<!---

<img alt="Ramen" src="Documentation/media/logo.svg" width="50%" height="50%">

[![CNCF Status](https://img.shields.io/badge/cncf%20status-graduated-blue.svg)](https://www.cncf.io/projects)
[![Build Status](https://jenkins.ramen.io/buildStatus/icon?job=ramendr/ramen/master)](https://jenkins.ramen.io/blue/organizations/jenkins/ramendr%2Framen/activity)
[![GitHub release](https://img.shields.io/github/release/ramendr/ramen/all.svg)](https://github.com/ramendr/ramen/releases)
[![Docker Pulls](https://img.shields.io/docker/pulls/ramen/ceph)](https://hub.docker.com/u/ramen)
[![Go Report Card](https://goreportcard.com/badge/github.com/ramendr/ramen)](https://goreportcard.com/report/github.com/ramendr/ramen)
[![CII Best Practices](https://bestpractices.coreinfrastructure.org/projects/1599/badge)](https://bestpractices.coreinfrastructure.org/projects/1599)
[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2Framendr%2Framen.svg?type=shield)](https://app.fossa.io/projects/git%2Bgithub.com%2Framendr%2Framen?ref=badge_shield)
[![Slack](https://slack.ramen.io/badge.svg)](https://slack.ramen.io)
[![Twitter Follow](https://img.shields.io/twitter/follow/ramen_io.svg?style=social&label=Follow)](https://twitter.com/intent/follow?screen_name=ramen_io&user_id=788180534543339520)

-->

# What is Ramen?

Ramen is an open source **disaster-recovery service** for stateful applications running on
OpenShift Container Storage (OCS).  Ramen provides a simplified interface to automate the life-cylce of an 
application during disastrous events; these include failover of an application to a peer cluster and failback
of the application back to the home cluster. Ramen provides cloud-native interfaces to enable a kubernetes
cluster administrator to setup and manage disaster recovery of stateful applications using OCS across a set of
independent kubernetes clusters. Ramen replicates k8s metadata of a group of PVs/PVCs of a given application
across peer clusters and manages OCS’ replication of data in those PVs across the same peer clusters.

Ramen integrates deeply into cloud native environments leveraging extension points to provide a seamless
experience for scheduling replication of PVs, lifecycle management, resource management, security, monitoring,
and user experience.

# Ramen Usecase
Consider a Kubernetes cluster that is running a stateful application; let us call this cluster the
home cluster for that application.  To prepare for a disastrous event affecting the home cluster region, one
needs to provision one or more peer clusters in alternate regions (away from the home cluster region) and
replicate the setup on home cluster to the peer cluster, which requires the following:
A. Replicate the application deployment configuration in the home cluster to the peer cluster.
   running in the home cluster to the peer clusters.  Ramen relies on Red Hat ACM to meet this requirement.
B. Replicate the data of all the PVs (PersistentVolumes) of the application in the home cluster to
   the one of the peer clusters.  Ramen relies on Red Hat OCS to meet this requirement.
C. Replicate the etcd metadata of all the PVs of the application in the home cluster to one of
   the peer clusters.  Ramen internally performs this task by using an S3 store to store-and-forward the
   metadata of all PVs of the application.

# Assumptions
- RHACM manages both the home and peer clusters that are in the DR relationship.
- Red Hat OCS is the cloud native storage provider for persistent volumes in both the home and peer clusters
  that are in the DR relationship.
- At any given time, the application is required to either run in the home cluster or in the peer cluster,
  not concurrently on both clusters.

# Prerequisites
- Ramen requires an S3 store to store-and-forward PV metadata

# Features
- Ramen supports OCS WAN DR async replication on a given RPO schedule.  In future, Ramen plans to support
  OCS Metro DR sync replication.
- For a given PV, Ramen supports one peer cluster per home cluster.  Ramen will support multiple peer clusters
  in future.

For more details about the storage solutions currently supported by Ramen, please refer to the
[project status section](#project-status) below.
We plan to continue adding support for other storage systems and environments based on community demand and
engagement in future releases. See our [roadmap](ROADMAP.md) for more details.

## Getting Started and Documentation

For installation, deployment, and administration, see our [Documentation](https://ramen.github.io/docs/ramen/master).

## Contributing

We welcome contributions. See [Contributing](CONTRIBUTING.md) to get started.

## Report a Bug

For filing bugs, suggesting improvements, or requesting new features, please open an [issue](https://github.com/ramendr/ramen/issues).

### Reporting Security Vulnerabilities

Please see [security release process](SECURITY.md).

## Contact

*Do we need this section now?*

Please use the following to reach members of the community:

- Slack: Join our [slack channel](https://slack.ramen.io)
- Forums: [ramen-dev](https://groups.google.com/forum/#!forum/ramen-dev)
- Twitter: [@ramen_io](https://twitter.com/ramen_io)

### Community Meeting
~~ Delete this section?~~

## Project Status

The status of each API supported by Ramen can be found in the table below.
Each API group is assigned its own individual status to reflect their varying maturity and stability.
More details about API versioning and status in Kubernetes can be found on the Kubernetes
[API versioning page](https://kubernetes.io/docs/concepts/overview/kubernetes-api/#api-versioning).

- **Alpha:** The API may change in incompatible ways in a later software release without notice, recommended for use only in short-lived testing clusters, due to increased risk of bugs and lack of long-term support.
- **Beta:** Support for the overall features will not be dropped, though details may change. Support for upgrading or migrating between versions will be provided, either through automation or manual steps.
- **Stable:** Features will appear in released software for many subsequent versions and support for upgrading between versions will be provided with software automation in the vast majority of scenarios.

| Name        | Details                                                                                                                                                                                                                                                                                                                | API Group                    | Status                                                                         |
| ----------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------- | ------------------------------------------------------------------------------ |
| AppVolumeReplication| Admin deploys this CR at RHACM hub to manage disaster recovery of an application across peer clusters in a disaster recovery relationship.| ramendr.openshift.io/v1              | AlphaV1                                                                         |
| VolumeReplicationGroup| RamenHub operator deploys this CR on the managed clusters to cover two aspects: (a) replication of k8s metadata of all PVs/PVCs of a given application across peer clusters in a disaster recovery setup, and (b) manage OCS' replication of data in those PVs across the clusters.| ramendr.openshift.io/v1              | AlphaV1                                                                         |

~~### Official Releases~~

~~Official releases of Ramen can be found on the [releases page](https://github.com/ramendr/ramen/releases).
Please note that it is **strongly recommended** that you use [official releases](https://github.com/ramendr/ramen/releases) of Ramen, as unreleased versions from the master branch are subject to changes and incompatibilities that will not be supported in the official releases.
Builds from the master branch can have functionality changed and even removed at any time without compatibility support and without prior notice.~~

## Licensing

Ramen is under the Apache 2.0 license.

<!---
[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2Framendr%2Framen.svg?type=large)](https://app.fossa.io/projects/git%2Bgithub.com%2Framendr%2Framen?ref=badge_large)
-->
