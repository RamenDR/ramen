<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# User quick start guide for testing application DR

This page will help you to set up an environment for testing **Regional-DR**
basic tests.

This video will demonstrate how to test application DR *after* completing the configuration.

[![Video demo](https://img.youtube.com/vi/8-fpChSWzeo/hqdefault.jpg)](https://www.youtube.com/embed/8-fpChSWzeo)

## What you’ll need

To set up a *Ramen* development environment you will need to run 3
Kubernetes clusters. *Ramen* makes this easy using minikube, but you need
enough resources:

- Bare metal or virtual machine with nested virtualization enabled
- 8 CPUs or more
- 20 GiB of free memory
- 100 GiB of free space
- Internet connection
- Linux - tested on *Fedora* 37 and 38
- non-root user with sudo privileges (all instructions are for non-root user)

## Setting up your machine

1. Fork the *Ramen* repository at [https://github.com/RamenDR/ramen](https://github.com/RamenDR/ramen)

1. Clone the source locally

   ```
   git clone https://github.com/{your_github_id}/ramen.git
   cd ramen
   ```

1. Create python virtual environment

   The *Ramen* project use python tool to create and provision test
   environment and run tests. The create a virtual environment including
   the tools run:

   ```
   make venv
   ```

   This create a virtual environment in `~/.venv/ramen` and a symbolic
   link `venv` for activating the environment.

   To activate the environment use:

   ```
   source venv
   ```

   To exit virtual environment issue command *deactivate*.

1. Enable libvirtd service if disabled.

   ```
   sudo systemctl enable libvirtd --now
   ```

   Verify libvirtd service is now active with no errors.

   ```
   sudo systemctl status libvirtd -l
   ```

1. Add yourself to the libvirt group (required for minikube kvm2 driver).

   ```
   sudo usermod -a -G libvirt $(whoami)
   ```

   Logout and login again for the change above to be in effect.

1. Install `@virtualization` group - on Fedora you can use:

   ```
   sudo dnf install @virtualization
   ```

   For more information see [Virtualization on Fedora](https://docs.fedoraproject.org/en-US/quick-docs/virtualization-getting-started/).

1. Install the `kubectl` tool. See
   [Install and Set Up kubectl on Linux](https://kubernetes.io/docs/tasks/tools/install-kubectl-linux/)
   for details.
   Tested with version v1.27.4.

1. Install minikube - on Fedora you can use::

   ```
   sudo dnf install https://storage.googleapis.com/minikube/releases/latest/minikube-latest.x86_64.rpm
   ```

   Tested with version v1.31.

1. Validate the installation

   Run the drenv-selftest to validate that we can create a test cluster:

   ```
   test/scripts/drenv-selftest
   ```

   Example output:

   ```
   1. Activating the ramen virtual environment ...


   2. Creating a test cluster ...

   2023-11-12 14:53:43,321 INFO    [drenv-selftest-vm] Starting environment
   2023-11-12 14:53:43,367 INFO    [drenv-selftest-cluster] Starting minikube cluster
   2023-11-12 14:54:15,331 INFO    [drenv-selftest-cluster] Cluster started in 31.96 seconds
   2023-11-12 14:54:15,332 INFO    [drenv-selftest-cluster/0] Running addons/example/start
   2023-11-12 14:54:33,181 INFO    [drenv-selftest-cluster/0] addons/example/start completed in 17.85 seconds
   2023-11-12 14:54:33,181 INFO    [drenv-selftest-cluster/0] Running addons/example/test
   2023-11-12 14:54:33,381 INFO    [drenv-selftest-cluster/0] addons/example/test completed in 0.20 seconds
   2023-11-12 14:54:33,381 INFO    [drenv-selftest-vm] Environment started in 50.06 seconds

   3. Deleting the test cluster ...

   2023-11-12 14:54:33,490 INFO    [drenv-selftest-vm] Deleting environment
   2023-11-12 14:54:33,492 INFO    [drenv-selftest-cluster] Deleting cluster
   2023-11-12 14:54:34,106 INFO    [drenv-selftest-cluster] Cluster deleted in 0.61 seconds
   2023-11-12 14:54:34,106 INFO    [drenv-selftest-vm] Environment deleted in 0.62 seconds

   drenv is set up properly
   ```

1. Install `clusteradm` tool. See
   [Install clusteradm CLI tool](https://open-cluster-management.io/getting-started/installation/start-the-control-plane/#install-clusteradm-cli-tool)
   for the details.
   Version v0.7.1 or later is required.

1. Install `subctl` tool, See
   [Submariner subctl installation](https://submariner.io/operations/deployment/subctl/)
   for the details.
   Tested with version v0.16.0.

1. Install the `velero` tool. See
   [Velero Basic Install](https://velero.io/docs/v1.12/basic-install/)
   for the details.
   Tested with version v1.12.0.

1. Install the `virtctl` tool. See
   [virtctl install](https://kubevirt.io/quickstart_minikube/#virtctl)
   for the details.
   Tested with version v1.0.1.

   ```
   curl -L -o virtctl https://github.com/kubevirt/kubevirt/releases/download/v1.0.1/virtctl-v1.0.1-linux-amd64
   ```

   After download completes for `virtctl` issue these commands.

   ```
   chmod +x virtctl
   sudo install virtctl /usr/local/bin
   ```

1. Install `helm` tool - on Fedora you can use:

   ```
   sudo dnf install helm
   ```

   Tested with version v3.11.

1. Install `podman` - on Fedora you can use:

   ```
   sudo dnf install podman
   ```

   Tested with version 4.7.0.

1. Install `golang` - on Fedora you can use:

   ```
   sudo dnf install golang
   ```

   Tested with version go1.20.10.

## Starting the test environment

Before using the `drenv` tool to start a test environment, you need to
activate the python virtual environment:

```
cd ramen
source venv
```

This environment simulates a Regional DR setup, when the managed clusters are
located in different zones (e.g. US, Europe) and storage is replicated
asynchronously. This environment includes 3 clusters - a hub cluster (`hub`),
and 2 managed clusters (`dr1`, `dr2`) using `Ceph` storage. The connectivity
between managed clusters will be configured using subctl (Submariner client).
In addition, rbd-mirroring is configured using the Submariner connection for
ceph image replication of block volumes.

To start the environment, run:

```
cd test
drenv start envs/regional-dr.yaml
```

Starting takes 10-15 minutes, depending on your machine and
internet connection.

Example output:

```
2023-11-02 05:26:41,729 INFO    [rdr] Starting environment
2023-11-02 05:26:41,860 INFO    [dr1] Starting minikube cluster
2023-11-02 05:26:42,948 INFO    [dr2] Starting minikube cluster
2023-11-02 05:26:43,929 INFO    [hub] Starting minikube cluster
2023-11-02 05:28:23,677 INFO    [dr1] Cluster started in 101.82 seconds
[...]
2023-11-02 05:40:40,777 INFO    [rdr] Dumping ramen e2e config to '/home/aclewett/.config/drenv/rdr'
2023-11-02 05:40:41,129 INFO    [rdr] Environment started in 839.40 seconds
```

The kubconfigs for dr1, dr2 and hub can be found in
$HOME/.config/drenv/rdr/kubeconfigs/.

Validate that the clusters are created.

```
miikube profile list
```

Example output:

```
|---------|-----------|------------|-----------------|------|---------|---------|-------|--------|
| Profile | VM Driver |  Runtime   |       IP        | Port | Version | Status  | Nodes | Active |
|---------|-----------|------------|-----------------|------|---------|---------|-------|--------|
| dr1     | kvm2      | containerd | 192.168.122.70  | 8443 | v1.27.4 | Running |     1 |        |
| dr2     | kvm2      | containerd | 192.168.122.51  | 8443 | v1.27.4 | Running |     1 |        |
| hub     | kvm2      | containerd | 192.168.122.162 | 8443 | v1.27.4 | Running |     1 |        |
|---------|-----------|------------|-----------------|------|---------|---------|-------|--------|
```

You can inspect the environment using these commands.

```
kubectl get deploy -A --context hub
kubectl get deploy -A --context dr1
kubectl get deploy -A --context dr2
```

## Build the ramen operator image

Build the *Ramen* operator container image:

```
cd ramen
make docker-build
```

Select `docker.io/library/golang:1.21` when prompted.

```
podman build -t quay.io/ramendr/ramen-operator:latest .
[1/2] STEP 1/9: FROM golang:1.21 AS builder
? Please select an image:
    registry.fedoraproject.org/golang:1.21
    registry.access.redhat.com/golang:1.21
  ▸ docker.io/library/golang:1.21
    quay.io/golang:1.21
```

This builds the image `quay.io/ramendr/ramen-operator:latest`

## Deploy and Configure the ramen operator

To deploy the *Ramen* operator in the test environment:

```
ramenctl deploy test/envs/regional-dr.yaml
```

For more info on the `ramenctl` tool see
[ramenctl/README.md](../ramenctl/README.md).

Ramen now needs to be configured to know about the managed clusters and the
s3 endpoints. To configure the ramen operator for test environment:

```
ramenctl config test/envs/regional-dr.yaml
```

## Running system tests

At this point *Ramen* is ready to protect workloads in your cluster, and
you are ready for testing basic flows.

To run basic tests using regional-dr environment run:

```
cd ramen
test/basic-test/run test/envs/regional-dr.yaml
```

This test does these operations:

1. Deploys an application using
   [ocm-ramen-samples repo](https://github.com/RamenDR/ocm-ramen-samples)
1. Enables DR for the application
1. Fails over the application to the other cluster
1. Relocates the application back to the original cluster
1. Disables DR for the application
1. Uninstalls the application

If needed, you can run one or more steps from this test, for example to
deploy and enable DR run:

```
env=$PWD/test/envs/regional-dr.yaml
test/basic-test/deploy $env
test/basic-test/enable-dr $env
```

At this point you can run run manually failover, relocate one or more
times as needed.

To clean up the basic-test for regional-dr.yaml run this command:

```
test/basic-test/undeploy $env
```

## Undeploying the test environment

If you want to clean up your environment completely run this command.
All clusters and resources will be deleted.

```
cd ramen/test
drenv delete envs/regional-dr.yaml
```
