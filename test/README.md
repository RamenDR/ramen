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
          - file: example/start
profiles:
  - name: ex1
    template: example-cluster
  - name: ex2
    template: example-cluster
workers:
  - scripts:
      - file: example/test
        args: ["ex1", "ex2"]
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
2022-11-23 23:35:35,960 INFO    [env] Using example.yaml
2022-11-23 23:35:35,964 INFO    [ex1] Starting cluster
2022-11-23 23:35:36,465 INFO    [ex2] Starting cluster
2022-11-23 23:36:22,041 INFO    [ex1] Cluster started in 46.08 seconds
2022-11-23 23:36:22,042 INFO    [ex1/0] Starting example/start
2022-11-23 23:36:22,394 INFO    [ex1/0] example/start completed in 0.35 seconds
2022-11-23 23:36:38,445 INFO    [ex2] Cluster started in 61.98 seconds
2022-11-23 23:36:38,445 INFO    [ex2/0] Starting example/start
2022-11-23 23:36:38,735 INFO    [ex2/0] example/start completed in 0.29 seconds
2022-11-23 23:36:38,946 INFO    [env/0] Starting example/test
2022-11-23 23:36:53,710 INFO    [env/0] example/test completed in 14.76 seconds
2022-11-23 23:36:53,710 INFO    [env] Started in 77.75 seconds
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
$ kubectl --context ex1 get pods
NAME                                 READY   STATUS    RESTARTS      AGE
example-deployment-fdf86bbfc-gtc8x   1/1     Running   1 (29s ago)   92s

$ kubectl --context ex2 get pods
NAME                                 READY   STATUS    RESTARTS      AGE
example-deployment-fdf86bbfc-sd9wt   1/1     Running   1 (33s ago)   97s
```

#### Running scripts manually

After starting both clusters, we run the `example/test` script. We can
run the script manually again to test the clusters:

```
$ example/test ex1 ex2
* Testing example deploymnet on cluster ex1
  deployment "example-deployment" successfully rolled out
* Testing example deploymnet on cluster ex2
  deployment "example-deployment" successfully rolled out
```

#### Starting a started environment

If something failed while starting, or we change the scripts, we can run
start again. This can be faster then creating the environment from
scratch.

```
$ drenv start example.yaml
2022-11-23 23:39:45,307 INFO    [env] Using example.yaml
2022-11-23 23:39:45,311 INFO    [ex1] Starting cluster
2022-11-23 23:39:45,811 INFO    [ex2] Starting cluster
2022-11-23 23:39:59,682 INFO    [ex1] Cluster started in 14.37 seconds
2022-11-23 23:39:59,682 INFO    [ex1] Waiting until all deployments are available
2022-11-23 23:40:01,761 INFO    [ex2] Cluster started in 15.95 seconds
2022-11-23 23:40:01,761 INFO    [ex2] Waiting until all deployments are available
2022-11-23 23:40:30,064 INFO    [ex1] Deployments are available in 30.38 seconds
2022-11-23 23:40:30,064 INFO    [ex1/0] Starting example/start
2022-11-23 23:40:30,295 INFO    [ex1/0] example/start completed in 0.23 seconds
2022-11-23 23:40:32,120 INFO    [ex2] Deployments are available in 30.36 seconds
2022-11-23 23:40:32,120 INFO    [ex2/0] Starting example/start
2022-11-23 23:40:32,363 INFO    [ex2/0] example/start completed in 0.24 seconds
2022-11-23 23:40:32,622 INFO    [env/0] Starting example/test
2022-11-23 23:40:32,919 INFO    [env/0] example/test completed in 0.30 seconds
2022-11-23 23:40:33,123 INFO    [env] Started in 47.81 seconds
```

#### Using --verbose option

While debugging it is useful to use the `--verbose` option to see much
more details:

```
$ drenv start example.yaml -v
2022-11-23 23:41:33,490 INFO    [env] Using example.yaml
2022-11-23 23:41:33,493 INFO    [ex1] Starting cluster
2022-11-23 23:41:33,644 DEBUG   [ex1] * [ex1] minikube v1.28.0 on Fedora 36
2022-11-23 23:41:33,645 DEBUG   [ex1]   - MINIKUBE_HOME=/data/minikube
2022-11-23 23:41:33,975 DEBUG   [ex1] * Using the kvm2 driver based on existing profile
2022-11-23 23:41:33,994 INFO    [ex2] Starting cluster
2022-11-23 23:41:33,995 DEBUG   [ex1] * Starting control plane node ex1 in cluster ex1
2022-11-23 23:41:34,048 DEBUG   [ex1] * Updating the running kvm2 "ex1" VM ...
2022-11-23 23:41:34,169 DEBUG   [ex2] * [ex2] minikube v1.28.0 on Fedora 36
2022-11-23 23:41:34,170 DEBUG   [ex2]   - MINIKUBE_HOME=/data/minikube
2022-11-23 23:41:34,521 DEBUG   [ex2] * Using the kvm2 driver based on existing profile
2022-11-23 23:41:34,542 DEBUG   [ex2] * Starting control plane node ex2 in cluster ex2
2022-11-23 23:41:35,185 DEBUG   [ex2] * Updating the running kvm2 "ex2" VM ...
2022-11-23 23:41:39,277 DEBUG   [ex1] * Preparing Kubernetes v1.25.3 on containerd 1.6.8 ...
2022-11-23 23:41:40,491 DEBUG   [ex2] * Preparing Kubernetes v1.25.3 on containerd 1.6.8 ...
2022-11-23 23:41:49,805 DEBUG   [ex1] * Configuring bridge CNI (Container Networking Interface) ...
2022-11-23 23:41:50,031 DEBUG   [ex1] * Verifying Kubernetes components...
2022-11-23 23:41:50,101 DEBUG   [ex1]   - Using image gcr.io/k8s-minikube/storage-provisioner:v5
2022-11-23 23:41:51,194 DEBUG   [ex1] * Enabled addons: storage-provisioner, default-storageclass
2022-11-23 23:41:51,250 DEBUG   [ex1] * Done! kubectl is now configured to use "ex1" cluster and "default" namespace by default
2022-11-23 23:41:51,278 INFO    [ex1] Cluster started in 17.78 seconds
2022-11-23 23:41:51,279 INFO    [ex1] Waiting until all deployments are available
2022-11-23 23:41:52,909 DEBUG   [ex2] * Configuring bridge CNI (Container Networking Interface) ...
2022-11-23 23:41:53,103 DEBUG   [ex2] * Verifying Kubernetes components...
2022-11-23 23:41:53,170 DEBUG   [ex2]   - Using image gcr.io/k8s-minikube/storage-provisioner:v5
2022-11-23 23:41:54,153 DEBUG   [ex2] * Enabled addons: storage-provisioner, default-storageclass
2022-11-23 23:41:54,212 DEBUG   [ex2] * Done! kubectl is now configured to use "ex2" cluster and "default" namespace by default
2022-11-23 23:41:54,233 INFO    [ex2] Cluster started in 20.24 seconds
2022-11-23 23:41:54,233 INFO    [ex2] Waiting until all deployments are available
2022-11-23 23:42:21,512 DEBUG   [ex1] deployment.apps/example-deployment condition met
2022-11-23 23:42:21,615 DEBUG   [ex1] deployment.apps/coredns condition met
2022-11-23 23:42:21,637 INFO    [ex1] Deployments are available in 30.36 seconds
2022-11-23 23:42:21,638 INFO    [ex1/0] Starting example/start
2022-11-23 23:42:21,671 DEBUG   [ex1/0] * Deploying example
2022-11-23 23:42:21,867 DEBUG   [ex1/0]   deployment.apps/example-deployment unchanged
2022-11-23 23:42:21,872 INFO    [ex1/0] example/start completed in 0.23 seconds
2022-11-23 23:42:24,464 DEBUG   [ex2] deployment.apps/example-deployment condition met
2022-11-23 23:42:24,565 DEBUG   [ex2] deployment.apps/coredns condition met
2022-11-23 23:42:24,590 INFO    [ex2] Deployments are available in 30.36 seconds
2022-11-23 23:42:24,590 INFO    [ex2/0] Starting example/start
2022-11-23 23:42:24,625 DEBUG   [ex2/0] * Deploying example
2022-11-23 23:42:24,823 DEBUG   [ex2/0]   deployment.apps/example-deployment unchanged
2022-11-23 23:42:24,827 INFO    [ex2/0] example/start completed in 0.24 seconds
2022-11-23 23:42:25,092 INFO    [env/0] Starting example/test
2022-11-23 23:42:25,157 DEBUG   [env/0] * Testing example deploymnet on cluster ex1
2022-11-23 23:42:25,284 DEBUG   [env/0]   deployment "example-deployment" successfully rolled out
2022-11-23 23:42:25,284 DEBUG   [env/0] * Testing example deploymnet on cluster ex2
2022-11-23 23:42:25,405 DEBUG   [env/0]   deployment "example-deployment" successfully rolled out
2022-11-23 23:42:25,410 INFO    [env/0] example/test completed in 0.32 seconds
2022-11-23 23:42:25,593 INFO    [env] Started in 52.10 seconds
```

#### Stopping the environment

We can stop the environment, for example if we need to reboot the host,
or don't have enough resources to run multiple environment at the same
time.

```
$ drenv stop example.yaml
2022-11-23 23:43:34,159 INFO    [env] Using example.yaml
2022-11-23 23:43:34,163 INFO    [ex1] Stopping cluster
2022-11-23 23:43:34,664 INFO    [ex2] Stopping cluster
2022-11-23 23:44:04,372 INFO    [ex1] Cluster stopped in 30.21 seconds
2022-11-23 23:44:07,819 INFO    [ex2] Cluster stopped in 33.16 seconds
2022-11-23 23:44:07,819 INFO    [env] Stopped in 33.66 seconds
```

We can start the environment later. This can be faster than recreating
it from scratch.

#### Deleting the environment

To delete the environment including the VM disks and dropping all
changes made to the environment:

```
$ drenv delete example.yaml
$ drenv delete example.yaml
2022-11-24 00:02:26,275 INFO    [env] Using example.yaml
2022-11-24 00:02:26,278 INFO    [ex1] Deleting cluster
2022-11-24 00:02:26,779 INFO    [ex2] Deleting cluster
2022-11-24 00:02:28,102 INFO    [ex1] Cluster deleted in 1.82 seconds
2022-11-24 00:02:28,103 INFO    [ex2] Cluster deleted in 1.32 seconds
2022-11-24 00:02:28,103 INFO    [env] Deleted in 1.83 seconds
```

### The environment file format

- `templates`: templates for creating new profiles.
    - `name`: profile name.
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
            - `file`: Script filename
            - `args`: Optional argument to the script. If not specified the
              script is run with one argument, the profile name.

- `profiles`: List of profile managed by the environment. Any template
   key is valid in the profile, overriding the same key from the template.
    - `template`: The template to create this profile from.

- `workers`: Optional list of workers for running scripts after all
  profile are started.
    - `name`: Optional worker name
    - `scripts`: Scripts to run by this worker
        - `file`: Script filename
        - `args`: Optional argument to the script. If not specified the
          script is run without any arguments.

#### Script arguments

When specifying script `args`, you can use the special variable `$name`.
This will be replaced with the profile name.

Example yaml:

```
profiles:
  - name: cluster1
    workers:
      - scripts:
          - file: script/start
            args: [$name, arg2]
```

This will run the script as:

```
script/start cluster1 arg2
```

## The regional-dr environment

This is a configuration for testing regional DR using a hub cluster and
2 managed clusters.

## Testing drenv

### Installing development tools

On Fedora run:

```
dnf install pytest python3-coverage
```

If pytest is not packaged for your distribution or the packaged version
is too old, you can install the latest version using pip - in the drenv
virtual environment:

```
pip install -r requirements.txt
```

### Running the tests

```
cd test
pytest
```

### Checking coverage

Running the tests collecting coverage data:

```
coverage run --source drenv -m pytest
```

Reporting coverage:

```
$ coverage report
Name                    Stmts   Miss  Cover
-------------------------------------------
drenv/__init__.py          82     62    24%
drenv/__main__.py         114    114     0%
drenv/envfile.py           61      0   100%
drenv/envfile_test.py      67      0   100%
-------------------------------------------
TOTAL                     324    176    46%
```

**Note**: if you installed coverage via pip, you will have to use
`coverage3` instead of `coverage`.

### Creating html coverage report

This creates html report and open the report in a browser:

```
coverage html
xdg-open htmlcov/index.html
```
