<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Ramen test environment

This directory provides tools and configuration for creating Ramen test
environment.

## Setup on Linux

1. Setup a development environment as describe in
   [developer quick start guide](../docs/devel-quick-start.md)

1. Add yourself to the libvirt group (required for minikube kvm2 driver).

   ```
   sudo usermod -a -G libvirt $(whoami)
   ```

   Logout and login again for the change above to be in effect.

1. Install minikube, for example on RHEL/CentOS/Fedora:

   ```
   sudo dnf install https://storage.googleapis.com/minikube/releases/latest/minikube-latest.x86_64.rpm
   ```

   Tested with version v1.33.1.

1. Install the `kubectl` tool. See
   [Install and Set Up kubectl on Linux](https://kubernetes.io/docs/tasks/tools/install-kubectl-linux/)
   Tested with version v1.30.2.

1. Install `clusteradm` tool. See
   [Install clusteradm CLI tool](https://open-cluster-management.io/getting-started/installation/start-the-control-plane/#install-clusteradm-cli-tool)
   for the details.
   Version v0.8.1 or later is reuired.

1. Install `subctl` tool, See
   [Submariner subctl installation](https://submariner.io/operations/deployment/subctl/)
   for the details.
   Version v0.18.0 or later is required.

1. Install the `velero` tool

   ```
   curl -L -o velero.tar.gz https://github.com/vmware-tanzu/velero/releases/download/v1.14.0/velero-v1.14.0-linux-amd64.tar.gz
   tar xf velero.tar.gz --strip 1 velero-v1.14.0-linux-amd64/velero
   sudo install velero /usr/local/bin
   rm velero.tar.gz velero
   ```

   For more info see
   [Velero Basic Install](https://velero.io/docs/v1.14/basic-install/)

1. Install `helm` tool - on Fedora you can use:

   ```
   sudo dnf install helm
   ```

   See [Installing Helm](https://helm.sh/docs/intro/install/) for other options.
   Tested with version v3.11.

1. Install the `virtctl` tool

   ```
   curl -L -o virtctl https://github.com/kubevirt/kubevirt/releases/download/v1.3.0/virtctl-v1.3.0-linux-amd64
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
   curl -L -o kubectl-gather https://github.com/nirs/kubectl-gather/releases/download/v0.5.1/kubectl-gather-v0.5.1-linux-amd64
   sudo install kubectl-gather /usr/local/bin
   rm kubectl-gather
   ```

   For more info see [kubectl-gather](https://github.com/nirs/kubectl-gather)

## Setup on macOS

1. Install the [Homebrew package manager](https://brew.sh/)

1. Install required packages

   ```
   brew install \
       argocd \
       go \
       helm \
       kubectl \
       kustomize \
       lima \
       minio-mc \
       velero \
       virtctl
   ```

   lima version 1.0.0 or later is required.

1. Install the `clusteradm` tool. See
   [Install clusteradm CLI tool](https://open-cluster-management.io/getting-started/installation/start-the-control-plane/#install-clusteradm-cli-tool)
   for the details. Version v0.8.1 or later is required.

1. Install the `subctl` tool, See
   [Submariner subctl installation](https://submariner.io/operations/deployment/subctl/)
   for the details. Version v0.18.0 or later is required.

1. Install the `kubectl-gather` plugin

   ```
   curl -L -o kubectl-gather https://github.com/nirs/kubectl-gather/releases/download/v0.5.1/kubectl-gather-v0.5.1-darwin-arm64
   sudo install kubectl-gather /usr/local/bin
   rm kubectl-gather
   ```

   For more info see [kubectl-gather](https://github.com/nirs/kubectl-gather)

1. Install `socket_vmnet` from source

   > [!IMPORTANT]
   > Do not install socket_vmnet from brew, it is insecure.

   ```
   git clone https://github.com/lima-vm/socket_vmnet.git
   cd socket_vmnet
   sudo make PREFIX=/opt/socket_vmnet install.bin
   sudo make PREFIX=/opt/socket_vmnet install.launchd
   ```

   For more info see [Installing socket_vmnet from source](https://github.com/lima-vm/socket_vmnet?tab=readme-ov-file#from-source)

## Testing that drenv is healthy

Run this script to make sure `drenv` works:

```
scripts/drenv-selftest
```

## Using the drenv tool

Before running the `drenv` tool you need to activate the virtual
environment:

```
source venv
```

The shell prompt will change to reflect that the `ramen` virtual
environment is active:

```
(ramen) [user@host ramen]$
```

Change directory to the test directory:

```
cd test
```

To set up the host for running specific drenv environment file,
run once before starting any environment:

```
drenv setup envs/regional-dr.yaml
```

The environment file describes one or more kubernetes clusters.

To start the environment:

```
drenv start envs/example.yaml
```

To stop the environment:

```
drenv stop envs/example.yaml
```

To delete the environment:

```
drenv delete envs/example.yaml
```

To inspect a processed environment file:

```
drenv dump envs/example.yaml
```

Dumping the file shows how drenv binds templates, expands addons
arguments, name workers, and applies default values. This can be useful
to debugging drenv or when writing a new environment file.

To see all available commands:

```
drenv --help
```

To see help for a command:

```
drenv start --help
```

When you are done you can deactivate the virtual environment:

```
deactivate
```

To clean up minikube changes done by `drenv setup`, run:

```
drenv cleanup
```

This should not be needed.

## Caching resources

If you run the drenv tool with a flaky network you can improve
reliability of starting the environment by caching resources.

To cache resources for the `regional-dr.yaml` environment run:

```
drenv cache envs/regional-dr.yaml
```

The cache expires in 2 days. To refresh the cache daily, you can install
a cron job to run `scripts/refresh-cache` daily as the user used to run
the environment.

See the `scripts/refresh-cache.crontab` for example user crontab.

To clear the cached resources run:

```
drenv clear
```

## The environment file

To create an environment you need an yaml file describing the
clusters and how to deploy them.

### Example environment file

```
name: example
templates:
  - name: "example-cluster"
    driver: podman
    container_runtime: cri-o
    workers:
      - addons:
          - name: example
profiles:
  - name: ex1
    template: example-cluster
  - name: ex2
    template: example-cluster
```

### Experimenting with the example environment

You can play with the example environment to understand how the `drenv`
tool works and how to write addons.

#### Starting the example environment

Starting the environment create 2 minikube clusters, deploy example
deployment on every clusters, and finally run a self test verifying that
the deployment is available on both clusters.

```
$ drenv start envs/example.yaml
2023-01-03 23:20:17,822 INFO    [example] Starting environment
2023-01-03 23:20:17,823 INFO    [ex1] Starting cluster
2023-01-03 23:20:18,824 INFO    [ex2] Starting cluster
2023-01-03 23:20:41,037 INFO    [ex1] Cluster started in 23.21 seconds
2023-01-03 23:20:41,038 INFO    [ex1/0] Running example/start
2023-01-03 23:20:41,200 INFO    [ex1/0] example/start completed in 0.16 seconds
2023-01-03 23:20:41,200 INFO    [ex1/0] Running example/test
2023-01-03 23:20:42,212 INFO    [ex2] Cluster started in 23.39 seconds
2023-01-03 23:20:42,212 INFO    [ex2/0] Running example/start
2023-01-03 23:20:42,387 INFO    [ex2/0] example/start completed in 0.17 seconds
2023-01-03 23:20:42,387 INFO    [ex2/0] Running example/test
2023-01-03 23:20:59,249 INFO    [ex1/0] example/test completed in 18.05 seconds
2023-01-03 23:21:01,474 INFO    [ex2/0] example/test completed in 19.09 seconds
2023-01-03 23:21:01,474 INFO    [example] Environment started in 43.65 seconds
```

#### Inspecting the clusters with minikube

We can use minikube to inspect or access the clusters:

```
$ minikube profile list
|---------|-----------|---------|--------------|------|---------|---------|-------|--------|
| Profile | VM Driver | Runtime |      IP      | Port | Version | Status  | Nodes | Active |
|---------|-----------|---------|--------------|------|---------|---------|-------|--------|
| ex1     | podman    | crio    | 192.168.49.2 | 8443 | v1.25.3 | Running |     1 |        |
| ex2     | podman    | crio    | 10.88.0.166  | 8443 | v1.25.3 | Running |     1 |        |
|---------|-----------|---------|--------------|------|---------|---------|-------|--------|
```

#### Inspecting the clusters with kubectl

We can use kubectl to access the clusters:

```
$ kubectl logs deploy/example-deployment --context ex1
Tue Jan  3 21:20:58 UTC 2023
Tue Jan  3 21:21:08 UTC 2023
Tue Jan  3 21:21:18 UTC 2023

$ kubectl logs deploy/example-deployment --context ex2
Tue Jan  3 21:21:00 UTC 2023
Tue Jan  3 21:21:10 UTC 2023
Tue Jan  3 21:21:20 UTC 2023
```

#### Isolating environments with --name-prefix

To run multiple instances of the same environment, or multiple
environments using the same profile names, use unique `--name-prefix`
for each run.

Start first instance:

```
$ drenv start --name-prefix test1- envs/example.yaml
2023-01-03 23:35:38,328 INFO    [test1-example] Starting environment
2023-01-03 23:35:38,330 INFO    [test1-ex1] Starting cluster
2023-01-03 23:35:39,330 INFO    [test1-ex2] Starting cluster
2023-01-03 23:36:01,923 INFO    [test1-ex1] Cluster started in 23.59 seconds
2023-01-03 23:36:01,924 INFO    [test1-ex1/0] Running example/start
2023-01-03 23:36:02,153 INFO    [test1-ex1/0] example/start completed in 0.23 seconds
2023-01-03 23:36:02,153 INFO    [test1-ex1/0] Running example/test
2023-01-03 23:36:02,428 INFO    [test1-ex2] Cluster started in 23.10 seconds
2023-01-03 23:36:02,429 INFO    [test1-ex2/0] Running example/start
2023-01-03 23:36:02,608 INFO    [test1-ex2/0] example/start completed in 0.18 seconds
2023-01-03 23:36:02,608 INFO    [test1-ex2/0] Running example/test
2023-01-03 23:36:21,114 INFO    [test1-ex1/0] example/test completed in 18.96 seconds
2023-01-03 23:36:22,616 INFO    [test1-ex2/0] example/test completed in 20.01 seconds
2023-01-03 23:36:22,616 INFO    [test1-example] Environment started in 44.29 seconds
```

This creates:

```
$ minikube profile list
|-----------|-----------|---------|--------------|------|---------|---------|-------|--------|
|  Profile  | VM Driver | Runtime |      IP      | Port | Version | Status  | Nodes | Active |
|-----------|-----------|---------|--------------|------|---------|---------|-------|--------|
| test1-ex1 | podman    | crio    | 192.168.49.2 | 8443 | v1.25.3 | Running |     1 |        |
| test1-ex2 | podman    | crio    | 10.88.0.196  | 8443 | v1.25.3 | Running |     1 |        |
|-----------|-----------|---------|--------------|------|---------|---------|-------|--------|
```

Start second instance:

```
$ drenv start --name-prefix test2- envs/example.yaml
2023-01-03 23:36:44,181 INFO    [test2-example] Starting environment
2023-01-03 23:36:44,182 INFO    [test2-ex1] Starting cluster
2023-01-03 23:36:45,183 INFO    [test2-ex2] Starting cluster
2023-01-03 23:37:08,685 INFO    [test2-ex2] Cluster started in 23.50 seconds
2023-01-03 23:37:08,686 INFO    [test2-ex2/0] Running example/start
2023-01-03 23:37:08,901 INFO    [test2-ex2/0] example/start completed in 0.22 seconds
2023-01-03 23:37:08,901 INFO    [test2-ex2/0] Running example/test
2023-01-03 23:37:08,969 INFO    [test2-ex1] Cluster started in 24.79 seconds
2023-01-03 23:37:08,969 INFO    [test2-ex1/0] Running example/start
2023-01-03 23:37:09,132 INFO    [test2-ex1/0] example/start completed in 0.16 seconds
2023-01-03 23:37:09,132 INFO    [test2-ex1/0] Running example/test
2023-01-03 23:37:26,811 INFO    [test2-ex2/0] example/test completed in 17.91 seconds
2023-01-03 23:37:27,119 INFO    [test2-ex1/0] example/test completed in 17.99 seconds
2023-01-03 23:37:27,119 INFO    [test2-example] Environment started in 42.94 seconds
```

This adds new profiles:

```
$ minikube profile list
|-----------|-----------|---------|--------------|------|---------|---------|-------|--------|
|  Profile  | VM Driver | Runtime |      IP      | Port | Version | Status  | Nodes | Active |
|-----------|-----------|---------|--------------|------|---------|---------|-------|--------|
| test1-ex1 | podman    | crio    | 192.168.49.2 | 8443 | v1.25.3 | Running |     1 |        |
| test1-ex2 | podman    | crio    | 10.88.0.196  | 8443 | v1.25.3 | Running |     1 |        |
| test2-ex1 | podman    | crio    | 192.168.58.2 | 8443 | v1.25.3 | Running |     1 |        |
| test2-ex2 | podman    | crio    | 10.88.0.201  | 8443 | v1.25.3 | Running |     1 |        |
|-----------|-----------|---------|--------------|------|---------|---------|-------|--------|
```

You must use the same `--name-prefix` when stopping or deleting the
environments.

#### Running addons hooks manually

When debugging addons hooks it is useful to run them manually:

```
$ example/start ex1
* Deploying example
  deployment.apps/example-deployment unchanged

$ example/test ex1
* Testing example deployment
  deployment "example-deployment" successfully rolled out
```

#### Starting a started environment

If something failed while starting, or we change the scripts, we can run
start again. This can be faster then creating the environment from
scratch.

```
$ drenv start envs/example.yaml
2023-01-03 23:40:25,451 INFO    [example] Starting environment
2023-01-03 23:40:25,452 INFO    [ex1] Starting cluster
2023-01-03 23:40:26,453 INFO    [ex2] Starting cluster
2023-01-03 23:40:29,972 INFO    [ex1] Cluster started in 4.52 seconds
2023-01-03 23:40:29,972 INFO    [ex1] Waiting until all deployments are available
2023-01-03 23:40:30,658 INFO    [ex2] Cluster started in 4.20 seconds
2023-01-03 23:40:30,658 INFO    [ex2] Waiting until all deployments are available
2023-01-03 23:41:00,224 INFO    [ex1] Deployments are available in 30.25 seconds
2023-01-03 23:41:00,225 INFO    [ex1/0] Running example/start
2023-01-03 23:41:00,381 INFO    [ex1/0] example/start completed in 0.16 seconds
2023-01-03 23:41:00,381 INFO    [ex1/0] Running example/test
2023-01-03 23:41:00,467 INFO    [ex1/0] example/test completed in 0.09 seconds
2023-01-03 23:41:00,925 INFO    [ex2] Deployments are available in 30.27 seconds
2023-01-03 23:41:00,925 INFO    [ex2/0] Running example/start
2023-01-03 23:41:01,080 INFO    [ex2/0] example/start completed in 0.15 seconds
2023-01-03 23:41:01,080 INFO    [ex2/0] Running example/test
2023-01-03 23:41:01,166 INFO    [ex2/0] example/test completed in 0.09 seconds
2023-01-03 23:41:01,166 INFO    [example] Environment started in 35.71 seconds
```

#### Using --verbose option

While debugging it is useful to use the `--verbose` option to see much
more details:

```
$ drenv start envs/example.yaml -v
2023-01-03 23:41:53,414 INFO    [example] Starting environment
2023-01-03 23:41:53,416 INFO    [ex1] Starting cluster
2023-01-03 23:41:53,539 DEBUG   [ex1] * [ex1] minikube v1.28.0 on Fedora 37
2023-01-03 23:41:53,540 DEBUG   [ex1]   - MINIKUBE_HOME=/data/minikube
2023-01-03 23:41:53,582 DEBUG   [ex1] * Using the podman driver based on user configuration
2023-01-03 23:41:53,664 DEBUG   [ex1] * Using Podman driver with root privileges
2023-01-03 23:41:53,666 DEBUG   [ex1] * Starting control plane node ex1 in cluster ex1
2023-01-03 23:41:53,669 DEBUG   [ex1] * Pulling base image ...
2023-01-03 23:41:53,672 DEBUG   [ex1] * Creating podman container (CPUs=2, Memory=4096MB) ...
2023-01-03 23:41:54,416 INFO    [ex2] Starting cluster
2023-01-03 23:41:54,614 DEBUG   [ex2] * [ex2] minikube v1.28.0 on Fedora 37
2023-01-03 23:41:54,617 DEBUG   [ex2]   - MINIKUBE_HOME=/data/minikube
2023-01-03 23:41:54,665 DEBUG   [ex2] * Using the podman driver based on user configuration
2023-01-03 23:41:54,768 DEBUG   [ex2] * Using Podman driver with root privileges
2023-01-03 23:41:54,771 DEBUG   [ex2] * Starting control plane node ex2 in cluster ex2
2023-01-03 23:41:54,774 DEBUG   [ex2] * Pulling base image ...
2023-01-03 23:41:54,777 DEBUG   [ex2] * Creating podman container (CPUs=2, Memory=4096MB) ...
2023-01-03 23:42:00,763 DEBUG   [ex1] * Preparing Kubernetes v1.25.3 on CRI-O 1.24.3 ...
2023-01-03 23:42:01,814 DEBUG   [ex1]   - Generating certificates and keys ...
2023-01-03 23:42:01,921 DEBUG   [ex2] * Preparing Kubernetes v1.25.3 on CRI-O 1.24.3 ...
2023-01-03 23:42:02,808 DEBUG   [ex2]   - Generating certificates and keys ...
2023-01-03 23:42:03,656 DEBUG   [ex1]   - Booting up control plane ...
2023-01-03 23:42:05,617 DEBUG   [ex2]   - Booting up control plane ...
2023-01-03 23:42:13,684 DEBUG   [ex1]   - Configuring RBAC rules ...
2023-01-03 23:42:14,095 DEBUG   [ex1] * Configuring CNI (Container Networking Interface) ...
2023-01-03 23:42:15,219 DEBUG   [ex1] * Verifying Kubernetes components...
2023-01-03 23:42:15,380 DEBUG   [ex1]   - Using image gcr.io/k8s-minikube/storage-provisioner:v5
2023-01-03 23:42:15,653 DEBUG   [ex2]   - Configuring RBAC rules ...
2023-01-03 23:42:15,752 DEBUG   [ex1] * Enabled addons: storage-provisioner, default-storageclass
2023-01-03 23:42:15,797 DEBUG   [ex1] * Done! kubectl is now configured to use "ex1" cluster and "default" namespace by default
2023-01-03 23:42:15,809 INFO    [ex1] Cluster started in 22.39 seconds
2023-01-03 23:42:15,809 INFO    [ex1/0] Running example/start
2023-01-03 23:42:15,843 DEBUG   [ex1/0] * Deploying example
2023-01-03 23:42:15,984 DEBUG   [ex1/0]   deployment.apps/example-deployment created
2023-01-03 23:42:15,992 INFO    [ex1/0] example/start completed in 0.18 seconds
2023-01-03 23:42:15,992 INFO    [ex1/0] Running example/test
2023-01-03 23:42:16,026 DEBUG   [ex1/0] * Testing example deployment
2023-01-03 23:42:16,067 DEBUG   [ex2] * Configuring CNI (Container Networking Interface) ...
2023-01-03 23:42:16,083 DEBUG   [ex1/0]   Waiting for deployment spec update to be observed...
2023-01-03 23:42:17,161 DEBUG   [ex2] * Verifying Kubernetes components...
2023-01-03 23:42:17,216 DEBUG   [ex2]   - Using image gcr.io/k8s-minikube/storage-provisioner:v5
2023-01-03 23:42:17,626 DEBUG   [ex2] * Enabled addons: storage-provisioner, default-storageclass
2023-01-03 23:42:17,675 DEBUG   [ex2] * Done! kubectl is now configured to use "ex2" cluster and "default" namespace by default
2023-01-03 23:42:17,688 INFO    [ex2] Cluster started in 23.27 seconds
2023-01-03 23:42:17,688 INFO    [ex2/0] Running example/start
2023-01-03 23:42:17,721 DEBUG   [ex2/0] * Deploying example
2023-01-03 23:42:17,858 DEBUG   [ex2/0]   deployment.apps/example-deployment created
2023-01-03 23:42:17,866 INFO    [ex2/0] example/start completed in 0.18 seconds
2023-01-03 23:42:17,866 INFO    [ex2/0] Running example/test
2023-01-03 23:42:17,900 DEBUG   [ex2/0] * Testing example deployment
2023-01-03 23:42:17,954 DEBUG   [ex2/0]   Waiting for deployment spec update to be observed...
2023-01-03 23:42:27,903 DEBUG   [ex1/0]   Waiting for deployment spec update to be observed...
2023-01-03 23:42:27,909 DEBUG   [ex1/0]   Waiting for deployment "example-deployment" rollout to finish: 0 out of 1 new replicas have been updated...
2023-01-03 23:42:28,021 DEBUG   [ex1/0]   Waiting for deployment "example-deployment" rollout to finish: 0 of 1 updated replicas are available...
2023-01-03 23:42:28,992 DEBUG   [ex2/0]   Waiting for deployment spec update to be observed...
2023-01-03 23:42:28,997 DEBUG   [ex2/0]   Waiting for deployment "example-deployment" rollout to finish: 0 out of 1 new replicas have been updated...
2023-01-03 23:42:29,046 DEBUG   [ex2/0]   Waiting for deployment "example-deployment" rollout to finish: 0 of 1 updated replicas are available...
2023-01-03 23:42:34,960 DEBUG   [ex1/0]   deployment "example-deployment" successfully rolled out
2023-01-03 23:42:34,967 INFO    [ex1/0] example/test completed in 18.98 seconds
2023-01-03 23:42:35,980 DEBUG   [ex2/0]   deployment "example-deployment" successfully rolled out
2023-01-03 23:42:35,987 INFO    [ex2/0] example/test completed in 18.12 seconds
2023-01-03 23:42:35,987 INFO    [example] Environment started in 42.57 seconds
```

#### Stopping the environment

We can stop the environment, for example if we need to reboot the host,
or don't have enough resources to run multiple environment at the same
time.

```
$ drenv stop envs/example.yaml
2023-01-03 23:43:09,169 INFO    [example] Stopping environment
2023-01-03 23:43:09,171 INFO    [ex1] Stopping cluster
2023-01-03 23:43:09,172 INFO    [ex2] Stopping cluster
2023-01-03 23:43:13,829 INFO    [ex1] Cluster stopped in 4.66 seconds
2023-01-03 23:43:14,032 INFO    [ex2] Cluster stopped in 4.86 seconds
2023-01-03 23:43:14,033 INFO    [example] Environment stopped in 4.86 seconds
```

We can start the environment later. This can be faster than recreating
it from scratch.

#### Deleting the environment

To delete the environment including the VM disks and dropping all
changes made to the environment:

```
$ drenv delete envs/example.yaml
2023-01-03 23:43:36,601 INFO    [example] Deleting environment
2023-01-03 23:43:36,602 INFO    [ex1] Deleting cluster
2023-01-03 23:43:36,603 INFO    [ex2] Deleting cluster
2023-01-03 23:43:43,645 INFO    [ex2] Cluster deleted in 7.04 seconds
2023-01-03 23:43:43,897 INFO    [ex1] Cluster deleted in 7.29 seconds
2023-01-03 23:43:43,897 INFO    [example] Environment deleted in 7.30 seconds
```

### The environment file format

- `templates`: templates for creating new profiles.
    - `name`: profile name.
    - `provider`: cluster provider. The default provider is "minikube",
      creating cluster using VM or containers.  Use "external" to use
      exsiting clusters not managed by `drenv`. Use the special value
      "$provider" to select the best provider for the host. (default
      "$provider")
    - `driver`: The minikube driver. On Linux, the default drivers are kvm2 and
      docker for VMs and containers. On MacOS, the defaults are hyperkit and
      podman. Use "$vm" and "$container" values to use the recommended VM and
      container drivers for the platform.
    - `container_runtime`: The container runtime to be used. Valid
      options: "docker", "cri-o", "containerd" (default: "containerd")
    - `network`: The network to run minikube with. If left empty, the behavior
      is same as that of minikube for the platform. Use
      "$network" value to use the recommended network configuration
      for the platform.
    - `extra_disks`: Number of extra disks (default 0)
    - `disk_size`: Disk size string (default "50g")
    - `nodes`: Number of cluster nodes (default 1)
    - `cni`: Network plugin (default "auto")
    - `cpus`: Number of CPUs per VM (default 2)
    - `memory`: Memory per VM (default 4g)
    - `addons`: List of minikube addons to install
    - `service_cluster_ip_range`: The CIDR to be used for service
      cluster IPs.
    - `extra_config`: List of extra config key=value. Each item adds
      `--extra-config` minikube option. See `minikube start --help` to
      see the possible keys and values.
    - `feature_gates`: List of Kubernetes feature gates key=value. Each
      item adds `--feature-gates` minikube option. See
      [Feature Gates](https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/)
      for possible keys and values.
    - `containerd`: Optional containerd configuration object. See
      `containerd config default` for available options.
    - `workers`: Optional list of workers to run when starting a
      profile. Use multiple workers to run scripts in parallel.
        - `name`: Optional worker name
        - `addons`: Addons to deploy by this worker.
            - `name`: Addon directory
            - `args`: Optional argument to addon hooks. If not specified
              the hooks are run with one argument, the profile name.

- `profiles`: List of profile managed by the environment. Any template
   key is valid in the profile, overriding the same key from the template.
    - `template`: The template to create this profile from.

- `workers`: Optional list of workers for deploying addons after all
  profile are started.
    - `name`: Optional worker name
    - `addons`: Addons to deploy by this worker
        - `name`: Addon directory
        - `args`: Optional argument to the addon hooks. If not specified
          the hooks are run without any arguments.

#### Addon hooks

The addon directory may contain hooks to be run on certain events, based
on the hook file name.

| Event        | Hooks         | Comment                             |
|--------------|---------------|-------------------------------------|
| start        | start, test   | after cluster was started           |
| stop         | stop          | before cluster is stopped           |
| delete       | -             |                                     |

The `start` and `test` hooks are not allowed to fail. If a hook fail,
execution stops and the entire command will fail.

The `stop` hook is allowed to fail. The failure is logged but the
`stop` command will not fail.

#### Addon arguments

When specifying addon `args`, you can use the special variable `$name`.
This will be replaced with the profile name.

Example yaml:

```
profiles:
  - name: cluster1
    workers:
      - addons:
          - name: my-addon
            args: [$name, arg2]
```

The `drenv` tool will run the hooks as:

```
my-addon/start cluster1 arg2
my-addon/test cluster1 arg2
```

#### Hook working directory

Hook should not assume the current working directory. To make the hook
runnable from any directory the hook can change the current working
directory to the hook directory:

```python
import os

os.chdir(os.path.dirname(__file__))
```

Now you can run the hook from any directory, and the hook can use
relative path for resources in the same directory:

```python
kubectl.apply("--filename=deployment.yaml", context=cluster)
```

#### containerd options

To configure containerd you can add a configuration object matching
containerd toml structure.

For example to enable this option containerd toml:

```toml
[plugins]
  [plugins."io.containerd.grpc.v1.cri"]
    device_ownership_from_security_context = true
```

Add this configuration to the profile:

```yaml
containerd:
  plugins:
    io.containerd.grpc.v1.cri:
      device_ownership_from_security_context: true
```

When set, contained configuration is merged into the current
configuration in `/etc/containerd/config.toml` in the node.

## Environment files

The environments files are located in the `envs` directory.

### Ramen testing environments

- `regional-dr.yaml` - for testing regional DR using a hub cluster and 2
  managed clusters with Ceph storage.

- `regional-dr-hubless.yaml` - for testing regional DR using a setup
  without a hub.

- `regional-dr-kubevirt.yaml` - for testing regional DR for kubevirt
  workloads.

- `regional-dr-external.yaml.example` - A starting point for creating
   environment for testing regional DR using with external storage.

### drenv development environments

These environments are useful for developing the `drenv` tool and
scripts. When debugging an issue or adding a new component, it is much
simpler and faster to work with a minimal environment.

- `vm.yaml` - for testing `drenv` with the $vm driver
- `container.yaml` - for testing `drenv` with the $container driver
- `example.yaml` - example for experimenting with the `drenv` tool
- `demo.yaml` - interactive demo for exploring the `drenv` tool
- `e2e.yaml` - example for testing integration with the e2e framework
- `external.yaml` - example for using external clusters
- `kubevirt.yaml` - for testing kubevirt and cdi addons
- `minio.yaml` - for testing `minio` deployment
- `ocm.yaml` - for testing `ocm` deployment
- `olm.yaml` - for testing `olm` deployment
- `rook.yaml` - for testing `rook` deployment
- `submariner.yaml` - for testing `submariner` deployment
- `velero.yaml` - for testing `velero` deployment
- `volsync.yaml` - for testing `volsync` deployment

## Testing drenv

### Preparing the test cluster

The tests requires a small test cluster. To create it use:

```
make cluster
```

This starts the `drenv-test-cluster` minikube profile using the kvm2
driver.

To delete the test cluster run:

```
make clean
```

### Running the tests

Run all linters and tests and report test coverage:

```
make
```

Create an html report and open the report in a browser:

```
make coverage-html
```

Checking that code is formatted according to project style:

```
make black
```

Reformatting code to be compatible with project style:

```
make black-reformat
```

## Writing environment tests

The `drenv` python package provides a `test` helper module to make
writing good environment test easy.

To create a new test, create a new directory in the test directory:

```
mkdir my-test
```

The directory can have one or more scripts as needed. The simplest test
will have only a `run` script, and a configuration file:

```
$ ls -1 my-test
config.yaml
run
```

The test must be runnable from any directory:

```
$ my-test/run
...
$ cd my-test
$ ./run
...
```

### Writing complicated tests

A more complicated test may have several steps. To keep the test simple
and easy understand and debug, separate each step in a test scrip that
can run by a developer manually. The `run` script will run all the steps
in the right order.

```
$ ls -1 basic-test/
config.yaml
deploy
failover
kustomization.yaml
relocate
run
undeploy
```

A developer can run one or more steps:

```
$ basic-test/deploy dr1; basic-test/failover dr2
...
```

Debug the system or the test, and continue:

```
$ basic-test/relocate dr1; basic-test/undeploy
...
```

Or run all the steps at once:

```
$ basic-test/run
...
```

### Writing a test script

A test script starts with importing the `drenv.test` module:

```python
from drenv import test
```

The first thing is to start the test:

```python
test.start("deploy", __file__)
```

This sets up the process for a new test:

- change directory to the parent directory of `__file__`
- load the configuration file from the test directory
- create a logger named "deploy" using standard log format
- create an arguments parser with the default options
- installs a hook for logging unhandled exception to the test log

If the test needs additional arguments it can add them using the same
arguments accepted by the standard library `argparse` module:

```python
test.add_argument("cluster", help="Cluster name to deploy on.")
```

Finally the test parses the arguments:

```python
args = test.parse_args()
```

If the command line arguments included the `-v' or '--verbose` option
the test logger level is increased to debug level automatically.

To access the test configuration, use:

```python
my_value = test.config["my-key"]
```

During the test, log important messages using:

```python
test.info("Starting deploy")
```

To log debug messages use:

```python
test.debug("Got reply: %s", reply)
```
