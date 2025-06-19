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

1. Install minikube - on Fedora you can use::

   ```
   sudo dnf install https://storage.googleapis.com/minikube/releases/latest/minikube-latest.x86_64.rpm
   ```

   Tested with version v1.36.0.

1. Install the `kubectl` tool

   ```
   curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
   sudo install kubectl /usr/local/bin
   rm kubectl
   ```

   For more info see
   [Install and Set Up kubectl on Linux](https://kubernetes.io/docs/tasks/tools/install-kubectl-linux/)
   Tested with version v1.33.2.

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

1. Install the `clusteradm` tool

   ```
   curl -L https://raw.githubusercontent.com/open-cluster-management-io/clusteradm/main/install.sh | bash -s 0.11.2
   ```

   For more info see
   [Install clusteradm CLI tool](https://open-cluster-management.io/docs/getting-started/installation/start-the-control-plane/#install-clusteradm-cli-tool).

   **WARNING**: clusteradm 0.11.1 is not compatible

1. Install the `subctl` tool

   ```
   curl -Ls https://get.submariner.io | bash
   sudo install .local/bin/subctl /usr/local/bin/
   rm .local/bin/subctl
   ```

   For more info see
   [Submariner subctl installation](https://submariner.io/operations/deployment/subctl/).
   Version v0.18.0 or later is required. Tested with version v0.20.1.

1. Install the `velero` tool

   ```
   curl -L -o velero.tar.gz https://github.com/vmware-tanzu/velero/releases/download/v1.14.0/velero-v1.14.0-linux-amd64.tar.gz
   tar xf velero.tar.gz --strip 1 velero-v1.14.0-linux-amd64/velero
   sudo install velero /usr/local/bin
   rm velero.tar.gz velero
   ```

   For more info see
   [Velero Basic Install](https://velero.io/docs/v1.14/basic-install/)

1. Install the `virtctl` tool.

   ```
   curl -L -o virtctl https://github.com/kubevirt/kubevirt/releases/download/v1.5.2/virtctl-v1.5.2-linux-amd64
   sudo install virtctl /usr/local/bin
   rm virtctl
   ```

   For more info see
   [virtctl install](https://kubevirt.io/quickstart_minikube/#virtctl)

1. Install `mc` tool

   ```
   curl -L -o mc https://dl.min.io/client/mc/release/linux-amd64/mc
   sudo install mc /usr/local/bin
   rm mc
   ```

   For more info see
   [MinIO Client Quickstart](https://min.io/docs/minio/linux/reference/minio-mc.html#quickstart)

1. Install `kustomize` tool

   ```
   curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" | bash
   sudo install kustomize /usr/local/bin
   rm kustomize
   ```

   For more info see
   [kustomize install](https://kubectl.docs.kubernetes.io/installation/kustomize/)

1. Install the `argocd` tool

   ```
   curl -L -o argocd https://github.com/argoproj/argo-cd/releases/download/v2.11.3/argocd-linux-amd64
   sudo install argocd /usr/local/bin/
   rm argocd
   ```

   For more info see [argocd installation](https://argo-cd.readthedocs.io/en/stable/cli_installation/)

1. Install the `kubectl-gather` plugin

   ```
   tag="$(curl -fsSL https://api.github.com/repos/nirs/kubectl-gather/releases/latest | jq -r .tag_name)"
   os="$(uname | tr '[:upper:]' '[:lower:]')"
   machine="$(uname -m)"
   if [ "$machine" = "aarch64" ]; then machine="arm64"; fi
   if [ "$machine" = "x86_64" ]; then machine="amd64"; fi
   curl -L -o kubectl-gather https://github.com/nirs/kubectl-gather/releases/download/$tag/kubectl-gather-$tag-$os-$machine
   sudo install kubectl-gather /usr/local/bin
   rm kubectl-gather
   ```

   kubectl-gather version 0.6.0 or later is required. Tested with
   version 0.8.0.
   For more info see [kubectl-gather](https://github.com/nirs/kubectl-gather)

1. Install `helm` tool - on Fedora you can use:

   ```
   sudo dnf install helm
   ```

   Tested with version v3.18.1.

1. Install `podman` - on Fedora you can use:

   ```
   sudo dnf install podman
   ```

   Tested with version 5.5.1.

1. Install `golang` - on Fedora you can use:

   ```
   sudo dnf install golang
   ```

   Tested with version go1.24.4.

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
minikube profile list
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
ramendev deploy test/envs/regional-dr.yaml
```

For more info on the `ramendev` tool see
[ramendev/README.md](../ramendev/README.md).

Ramen now needs to be configured to know about the managed clusters and the
s3 endpoints. To configure the ramen operator for test environment:

```
ramendev config test/envs/regional-dr.yaml
```

## Running system tests

At this point *Ramen* is ready to protect workloads in your cluster, and
you are ready for testing basic flows.

Ramen basic test use the [ocm-ramen-samples repo](https://github.com/RamenDR/ocm-ramen-samples).
Before running the tests, you need to deploy a channel pointing this
repo:

```
kubectl apply -k https://github.com/RamenDR/ocm-ramen-samples.git/channel --context hub
```

To run basic tests using regional-dr environment run:

```
test/basic-test/run test/envs/regional-dr.yaml
```

This test does these operations:

1. Deploys a busybox application
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
