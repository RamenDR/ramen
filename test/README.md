<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Ramen test environment

This directory provides tools and configuration for creating Ramen test
environment.

## Setup

1. Add yourself to the libvirt group (required for minikube kvm2 driver).

   ```
   sudo usermod -a -G libvirt $(whoami)
   ```

1. Logout and login again for the change above to be in effect.

1. Install minikube, for example on RHEL/CentOS/Fedora:

   ```
   sudo dnf install https://storage.googleapis.com/minikube/releases/latest/minikube-latest.x86_64.rpm
   ```

   You need `minikube` version supporting the `--extra-disks` option.
   The tool was tested with `minikube` v1.26.1.

1. Install the kubectl tool. See
   [Install and Set Up kubectl on Linux](https://kubernetes.io/docs/tasks/tools/install-kubectl-linux/)

1. Install `clusteradm` tool. See
   [Open Cluster Management Quick Start guide](https://open-cluster-management.io/getting-started/quick-start/#install-clusteradm-cli-tool)
   for the details.

1. Install the `drenv` package in a virtual environment:

   Run this once in the root of the source tree:

   ```
   python3 -m venv ~/.venv/ramen
   source ~/.venv/ramen/bin/activate
   pip install --upgrade pip
   pip install -e ./test
   ```

   You can create the virtual environment anywhere, but keeping the
   environment outside of the source tree is good practice.

   This installs a link file in the virtual environment:

   ```
   $ cat /home/nsoffer/.venv/ramen/lib/python3.10/site-packages/drenv.egg-link
   /home/nsoffer/src/ramen/test
   ```

   So changes in the `test/drenv` package are available immediately
   without installing the package again.

## Using the drenv tool

Before running the `drenv` tool you need to activate the virtual
environment:

```
source ~/.venv/ramen/bin/activate
```

The shell prompt will change to reflect that the `ramen` virtual
environment is active:

```
(ramen) [user@host test]$
```

Change directory to the test directory where the environment yamls and
scripts are:

```
cd test
```

To start the environment:

```
drenv start example.yaml
```

To stop the environment:

```
drenv stop example.yaml
```

To delete the environment:

```
drenv delete example.yaml
```

To inspect a processed environment file:

```
drenv dump example.yaml
```

Dumping the file shows how drenv binds templates, expands scripts
arguments, name workers, and applies default values. This can be useful
to debugging drenv or when writing a new environment file.

Useful options:

- `-v`, `--verbose`: Show verbose logs
- `-h`, `--help`: Show online help
- `--name-prefix`: Add prefix to profiles names

When you are done you can deactivate the virtual environment:

```
deactivate
```

## The environment file

To create an environment you need an yaml file describing the
clusters and how to deploy them.

### Example environment file

```
name: example
templates:
  - name: "example-cluster"
    workers:
      - scripts:
          - name: example
profiles:
  - name: ex1
    template: example-cluster
  - name: ex2
    template: example-cluster
```

### Experimenting with the example environment

You can play with the example environment to understand how the `drenv`
tool works and how to write scripts.

#### Starting the example environment

Starting the environment create 2 minikube clusters, deploy example
deployment on every clusters, and finally run a self test verifying that
the deployment is available on both clusters.

```
$ drenv start example.yaml
2022-12-01 23:30:25,294 INFO    [example] Starting environment
2022-12-01 23:30:25,295 INFO    [ex1] Starting cluster
2022-12-01 23:30:26,296 INFO    [ex2] Starting cluster
2022-12-01 23:31:04,112 INFO    [ex1] Cluster started in 38.82 seconds
2022-12-01 23:31:04,113 INFO    [ex1/0] Running example/start
2022-12-01 23:31:04,366 INFO    [ex1/0] example/start completed in 0.25 seconds
2022-12-01 23:31:04,366 INFO    [ex1/0] Running example/test
2022-12-01 23:31:18,404 INFO    [ex1/0] example/test completed in 14.04 seconds
2022-12-01 23:31:18,925 INFO    [ex2] Cluster started in 52.63 seconds
2022-12-01 23:31:18,925 INFO    [ex2/0] Running example/start
2022-12-01 23:31:19,152 INFO    [ex2/0] example/start completed in 0.23 seconds
2022-12-01 23:31:19,152 INFO    [ex2/0] Running example/test
2022-12-01 23:31:34,413 INFO    [ex2/0] example/test completed in 15.26 seconds
2022-12-01 23:31:34,414 INFO    [example] Environment started in 69.12 seconds
```

#### Inspecting the clusters with minikube

We can use minikube to inspect or access the clusters:

```
$ minikube profile list
|---------|-----------|------------|----------------|------|---------|---------|-------|--------|
| Profile | VM Driver |  Runtime   |       IP       | Port | Version | Status  | Nodes | Active |
|---------|-----------|------------|----------------|------|---------|---------|-------|--------|
| ex1     | kvm2      | containerd | 192.168.39.198 | 8443 | v1.25.3 | Running |     1 |        |
| ex2     | kvm2      | containerd | 192.168.50.184 | 8443 | v1.25.3 | Running |     1 |        |
|---------|-----------|------------|----------------|------|---------|---------|-------|--------|
```

#### Inspecting the clusters with kubectl

We can use kubectl to access the clusters:

```
$ kubectl logs deploy/example-deployment --context ex1
Thu Dec  1 21:31:18 UTC 2022
Thu Dec  1 21:31:28 UTC 2022
Thu Dec  1 21:31:38 UTC 2022

$ kubectl logs deploy/example-deployment --context ex2
Thu Dec  1 21:31:34 UTC 2022
Thu Dec  1 21:31:44 UTC 2022
Thu Dec  1 21:31:54 UTC 2022
```

#### Isolating environments with --name-prefix

To run multiple instances of the same environment, or multiple
environments using the same profile names, use unique `--name-prefix`
for each run.

Start first instance:

```
$ drenv start --name-prefix test1- example.yaml
2022-12-01 23:35:39,919 INFO    [test1-example] Starting environment
2022-12-01 23:35:39,921 INFO    [test1-ex1] Starting cluster
2022-12-01 23:35:40,923 INFO    [test1-ex2] Starting cluster
2022-12-01 23:36:19,035 INFO    [test1-ex1] Cluster started in 39.11 seconds
2022-12-01 23:36:19,035 INFO    [test1-ex1/0] Running example/start
2022-12-01 23:36:19,257 INFO    [test1-ex1/0] example/start completed in 0.22 seconds
2022-12-01 23:36:19,257 INFO    [test1-ex1/0] Running example/test
2022-12-01 23:36:34,458 INFO    [test1-ex1/0] example/test completed in 15.20 seconds
2022-12-01 23:36:37,469 INFO    [test1-ex2] Cluster started in 56.55 seconds
2022-12-01 23:36:37,469 INFO    [test1-ex2/0] Running example/start
2022-12-01 23:36:37,737 INFO    [test1-ex2/0] example/start completed in 0.27 seconds
2022-12-01 23:36:37,737 INFO    [test1-ex2/0] Running example/test
2022-12-01 23:36:52,942 INFO    [test1-ex2/0] example/test completed in 15.21 seconds
2022-12-01 23:36:52,942 INFO    [test1-example] Environment started in 73.02 seconds
```

This creates:

```
$ minikube profile list
|-----------|-----------|------------|---------------|------|---------|---------|-------|--------|
|  Profile  | VM Driver |  Runtime   |      IP       | Port | Version | Status  | Nodes | Active |
|-----------|-----------|------------|---------------|------|---------|---------|-------|--------|
| test1-ex1 | kvm2      | containerd | 192.168.39.54 | 8443 | v1.25.3 | Running |     1 |        |
| test1-ex2 | kvm2      | containerd | 192.168.50.76 | 8443 | v1.25.3 | Running |     1 |        |
|-----------|-----------|------------|---------------|------|---------|---------|-------|--------|
```

Start second instance:

```
$ drenv start --name-prefix test2- example.yaml
2022-12-01 23:38:01,812 INFO    [test2-example] Starting environment
2022-12-01 23:38:01,814 INFO    [test2-ex1] Starting cluster
2022-12-01 23:38:02,815 INFO    [test2-ex2] Starting cluster
2022-12-01 23:38:40,707 INFO    [test2-ex1] Cluster started in 38.89 seconds
2022-12-01 23:38:40,707 INFO    [test2-ex1/0] Running example/start
2022-12-01 23:38:40,980 INFO    [test2-ex1/0] example/start completed in 0.27 seconds
2022-12-01 23:38:40,980 INFO    [test2-ex1/0] Running example/test
2022-12-01 23:38:54,988 INFO    [test2-ex1/0] example/test completed in 14.01 seconds
2022-12-01 23:38:56,653 INFO    [test2-ex2] Cluster started in 53.84 seconds
2022-12-01 23:38:56,653 INFO    [test2-ex2/0] Running example/start
2022-12-01 23:38:56,890 INFO    [test2-ex2/0] example/start completed in 0.24 seconds
2022-12-01 23:38:56,890 INFO    [test2-ex2/0] Running example/test
2022-12-01 23:39:12,155 INFO    [test2-ex2/0] example/test completed in 15.26 seconds
2022-12-01 23:39:12,156 INFO    [test2-example] Environment started in 70.34 seconds
```

This adds new profiles:

```
$ minikube profile list
|-----------|-----------|------------|----------------|------|---------|---------|-------|--------|
|  Profile  | VM Driver |  Runtime   |       IP       | Port | Version | Status  | Nodes | Active |
|-----------|-----------|------------|----------------|------|---------|---------|-------|--------|
| test1-ex1 | kvm2      | containerd | 192.168.39.54  | 8443 | v1.25.3 | Running |     1 |        |
| test1-ex2 | kvm2      | containerd | 192.168.50.76  | 8443 | v1.25.3 | Running |     1 |        |
| test2-ex1 | kvm2      | containerd | 192.168.72.116 | 8443 | v1.25.3 | Running |     1 |        |
| test2-ex2 | kvm2      | containerd | 192.168.83.20  | 8443 | v1.25.3 | Running |     1 |        |
|-----------|-----------|------------|----------------|------|---------|---------|-------|--------|
```

You must use the same `--name-prefix` when stopping or deleting the
environments.

#### Running scripts manually

When debugging scripts it is useful to run them manually:

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
$ drenv start example.yaml
2022-12-01 23:41:15,543 INFO    [example] Starting environment
2022-12-01 23:41:15,545 INFO    [ex1] Starting cluster
2022-12-01 23:41:16,546 INFO    [ex2] Starting cluster
2022-12-01 23:41:29,160 INFO    [ex1] Cluster started in 13.62 seconds
2022-12-01 23:41:29,160 INFO    [ex1] Waiting until all deployments are available
2022-12-01 23:41:29,874 INFO    [ex2] Cluster started in 13.33 seconds
2022-12-01 23:41:29,874 INFO    [ex2] Waiting until all deployments are available
2022-12-01 23:41:59,512 INFO    [ex1] Deployments are available in 30.35 seconds
2022-12-01 23:41:59,513 INFO    [ex1/0] Running example/start
2022-12-01 23:41:59,711 INFO    [ex1/0] example/start completed in 0.20 seconds
2022-12-01 23:41:59,711 INFO    [ex1/0] Running example/test
2022-12-01 23:41:59,839 INFO    [ex1/0] example/test completed in 0.13 seconds
2022-12-01 23:42:00,228 INFO    [ex2] Deployments are available in 30.35 seconds
2022-12-01 23:42:00,228 INFO    [ex2/0] Running example/start
2022-12-01 23:42:00,431 INFO    [ex2/0] example/start completed in 0.20 seconds
2022-12-01 23:42:00,431 INFO    [ex2/0] Running example/test
2022-12-01 23:42:00,560 INFO    [ex2/0] example/test completed in 0.13 seconds
2022-12-01 23:42:00,560 INFO    [example] Environment started in 45.02 seconds
```

#### Using --verbose option

While debugging it is useful to use the `--verbose` option to see much
more details:

```
$ drenv start example.yaml -v
2022-12-01 23:42:38,883 INFO    [example] Starting environment
2022-12-01 23:42:38,885 INFO    [ex1] Starting cluster
2022-12-01 23:42:39,014 DEBUG   [ex1] * [ex1] minikube v1.28.0 on Fedora 36
2022-12-01 23:42:39,017 DEBUG   [ex1]   - MINIKUBE_HOME=/data/minikube
2022-12-01 23:42:39,263 DEBUG   [ex1] * Using the kvm2 driver based on user configuration
2022-12-01 23:42:39,279 DEBUG   [ex1] * Starting control plane node ex1 in cluster ex1
2022-12-01 23:42:39,283 DEBUG   [ex1] * Creating kvm2 VM (CPUs=2, Memory=4096MB, Disk=20480MB) ...
2022-12-01 23:42:39,886 INFO    [ex2] Starting cluster
2022-12-01 23:42:40,040 DEBUG   [ex2] * [ex2] minikube v1.28.0 on Fedora 36
2022-12-01 23:42:40,041 DEBUG   [ex2]   - MINIKUBE_HOME=/data/minikube
2022-12-01 23:42:40,336 DEBUG   [ex2] * Using the kvm2 driver based on user configuration
2022-12-01 23:42:40,357 DEBUG   [ex2] * Starting control plane node ex2 in cluster ex2
2022-12-01 23:42:55,748 DEBUG   [ex2] * Creating kvm2 VM (CPUs=2, Memory=4096MB, Disk=20480MB) ...
2022-12-01 23:43:05,054 DEBUG   [ex1] * Preparing Kubernetes v1.25.3 on containerd 1.6.8 ...
2022-12-01 23:43:06,214 DEBUG   [ex1]   - Generating certificates and keys ...
2022-12-01 23:43:08,111 DEBUG   [ex1]   - Booting up control plane ...
2022-12-01 23:43:15,196 DEBUG   [ex1]   - Configuring RBAC rules ...
2022-12-01 23:43:15,622 DEBUG   [ex1] * Configuring bridge CNI (Container Networking Interface) ...
2022-12-01 23:43:16,249 DEBUG   [ex1] * Verifying Kubernetes components...
2022-12-01 23:43:16,327 DEBUG   [ex1]   - Using image gcr.io/k8s-minikube/storage-provisioner:v5
2022-12-01 23:43:17,192 DEBUG   [ex1] * Enabled addons: storage-provisioner, default-storageclass
2022-12-01 23:43:17,245 DEBUG   [ex1] * Done! kubectl is now configured to use "ex1" cluster and "default" namespace by default
2022-12-01 23:43:17,269 INFO    [ex1] Cluster started in 38.38 seconds
2022-12-01 23:43:17,270 INFO    [ex1/0] Running example/start
2022-12-01 23:43:17,300 DEBUG   [ex1/0] * Deploying example
2022-12-01 23:43:17,550 DEBUG   [ex1/0]   deployment.apps/example-deployment created
2022-12-01 23:43:17,554 INFO    [ex1/0] example/start completed in 0.28 seconds
2022-12-01 23:43:17,554 INFO    [ex1/0] Running example/test
2022-12-01 23:43:17,580 DEBUG   [ex1/0] * Testing example deployment
2022-12-01 23:43:21,066 DEBUG   [ex2] * Preparing Kubernetes v1.25.3 on containerd 1.6.8 ...
2022-12-01 23:43:22,119 DEBUG   [ex2]   - Generating certificates and keys ...
2022-12-01 23:43:24,112 DEBUG   [ex2]   - Booting up control plane ...
2022-12-01 23:43:31,671 DEBUG   [ex2]   - Configuring RBAC rules ...
2022-12-01 23:43:32,082 DEBUG   [ex2] * Configuring bridge CNI (Container Networking Interface) ...
2022-12-01 23:43:32,706 DEBUG   [ex2] * Verifying Kubernetes components...
2022-12-01 23:43:32,769 DEBUG   [ex2]   - Using image gcr.io/k8s-minikube/storage-provisioner:v5
2022-12-01 23:43:33,553 DEBUG   [ex2] * Enabled addons: storage-provisioner, default-storageclass
2022-12-01 23:43:33,601 DEBUG   [ex2] * Done! kubectl is now configured to use "ex2" cluster and "default" namespace by default
2022-12-01 23:43:33,621 INFO    [ex2] Cluster started in 53.74 seconds
2022-12-01 23:43:33,621 INFO    [ex2/0] Running example/start
2022-12-01 23:43:33,624 DEBUG   [ex1/0]   Waiting for deployment spec update to be observed...
2022-12-01 23:43:33,624 DEBUG   [ex1/0]   Waiting for deployment spec update to be observed...
2022-12-01 23:43:33,624 DEBUG   [ex1/0]   Waiting for deployment "example-deployment" rollout to finish: 0 out of 1 new replicas have been updated...
2022-12-01 23:43:33,624 DEBUG   [ex1/0]   Waiting for deployment "example-deployment" rollout to finish: 0 of 1 updated replicas are available...
2022-12-01 23:43:33,624 DEBUG   [ex1/0]   deployment "example-deployment" successfully rolled out
2022-12-01 23:43:33,629 INFO    [ex1/0] example/test completed in 16.08 seconds
2022-12-01 23:43:33,651 DEBUG   [ex2/0] * Deploying example
2022-12-01 23:43:33,853 DEBUG   [ex2/0]   deployment.apps/example-deployment created
2022-12-01 23:43:33,857 INFO    [ex2/0] example/start completed in 0.24 seconds
2022-12-01 23:43:33,857 INFO    [ex2/0] Running example/test
2022-12-01 23:43:33,883 DEBUG   [ex2/0] * Testing example deployment
2022-12-01 23:43:49,131 DEBUG   [ex2/0]   Waiting for deployment spec update to be observed...
2022-12-01 23:43:49,131 DEBUG   [ex2/0]   Waiting for deployment spec update to be observed...
2022-12-01 23:43:49,132 DEBUG   [ex2/0]   Waiting for deployment "example-deployment" rollout to finish: 0 out of 1 new replicas have been updated...
2022-12-01 23:43:49,132 DEBUG   [ex2/0]   Waiting for deployment "example-deployment" rollout to finish: 0 of 1 updated replicas are available...
2022-12-01 23:43:49,132 DEBUG   [ex2/0]   deployment "example-deployment" successfully rolled out
2022-12-01 23:43:49,136 INFO    [ex2/0] example/test completed in 15.28 seconds
2022-12-01 23:43:49,136 INFO    [example] Environment started in 70.25 seconds
```

#### Stopping the environment

We can stop the environment, for example if we need to reboot the host,
or don't have enough resources to run multiple environment at the same
time.

```
$ drenv stop example.yaml
2022-12-01 23:47:20,688 INFO    [example] Stopping environment
2022-12-01 23:47:20,689 INFO    [ex1] Stopping cluster
2022-12-01 23:47:20,689 INFO    [ex2] Stopping cluster
2022-12-01 23:47:21,926 INFO    [ex2] Cluster stopped in 1.24 seconds
2022-12-01 23:47:21,931 INFO    [ex1] Cluster stopped in 1.24 seconds
2022-12-01 23:47:21,931 INFO    [example] Environment stopped in 1.24 seconds
```

We can start the environment later. This can be faster than recreating
it from scratch.

#### Deleting the environment

To delete the environment including the VM disks and dropping all
changes made to the environment:

```
$ drenv delete example.yaml
2022-12-01 23:49:54,853 INFO    [example] Deleting environment
2022-12-01 23:49:54,855 INFO    [ex1] Deleting cluster
2022-12-01 23:49:54,855 INFO    [ex2] Deleting cluster
2022-12-01 23:49:55,942 INFO    [ex1] Cluster deleted in 1.09 seconds
2022-12-01 23:49:55,963 INFO    [ex2] Cluster deleted in 1.11 seconds
2022-12-01 23:49:55,963 INFO    [example] Environment deleted in 1.11 seconds
```

### The environment file format

- `templates`: templates for creating new profiles.
    - `name`: profile name.
    - `driver`: The minikube driver. Tested with "kvm2" and "podman"
      (default "kvm2")
    - `container_runtime`: The container runtime to be used. Valid
      options: "docker", "cri-o", "containerd" (default: "containerd")
    - `network`: The network to run minikube with. If left empty,
      minikube will create a new isolated network.
    - `extra_disks`: Number of extra disks (default 0)
    - `disk_size`: Disk size string (default "50g")
    - `nodes`: Number of cluster nodes (default 1)
    - `cni`: Network plugin (default "auto")
    - `cpus`: Number of CPUs per VM (default 2)
    - `memory`: Memory per VM (default 4g)
    - `addons`: List of minikube addons to install
    - `workers`: Optional list of workers to run when starting a
      profile. Use multiple workers to run scripts in parallel.
        - `name`: Optional worker name
        - `scripts`: Scripts to run by this worker.
            - `name`: Scripts directory
            - `args`: Optional argument to the script. If not specified the
              script is run with one argument, the profile name.

- `profiles`: List of profile managed by the environment. Any template
   key is valid in the profile, overriding the same key from the template.
    - `template`: The template to create this profile from.

- `workers`: Optional list of workers for running scripts after all
  profile are started.
    - `name`: Optional worker name
    - `scripts`: Scripts to run by this worker
        - `name`: Scripts directory
        - `args`: Optional argument to the script. If not specified the
          script is run without any arguments.

#### Scripts hooks

The script direcotry may contain scripts to be run on certain events,
based on the hook file name.

| Event        | Scripts       |
|--------------|---------------|
| start        | start, test   |
| stop         | -             |
| delete       | -             |

#### Script arguments

When specifying script `args`, you can use the special variable `$name`.
This will be replaced with the profile name.

Example yaml:

```
profiles:
  - name: cluster1
    workers:
      - scripts:
          - name: script
            args: [$name, arg2]
```

The `drenv` tool will run the script hooks as:

```
script/start cluster1 arg2
script/test cluster1 arg2
```

## The regional-dr environment

This is a configuration for testing regional DR using a hub cluster and
2 managed clusters.

## Testing drenv

### Installing development tools

```
pip install -r requirements.txt
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
