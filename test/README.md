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
# Example environment.

profiles:
  - name: "ex1"
    scripts:
      - file: example/start
  - name: "ex2"
    scripts:
      - file: example/start
scripts:
  - file: example/test
    args: ["ex1", "ex2"]
```

### Experimenting with the example environment

You can play with the example environment to understand how the `drenv`
tool works and how to write scripts.

Starting the example environment:

```
$ drenv start example.yaml
2022-09-22 20:47:32,368 INFO    [env] Using example.yaml
2022-09-22 20:47:32,371 INFO    [ex1] Starting cluster
2022-09-22 20:47:32,371 INFO    [ex2] Starting cluster
2022-09-22 20:48:10,538 INFO    [ex2] Cluster started in 38.17 seconds
2022-09-22 20:48:10,538 INFO    [ex2] Starting example/start
2022-09-22 20:48:10,754 INFO    [ex2] example/start completed in 0.22 seconds
2022-09-22 20:48:27,509 INFO    [ex1] Cluster started in 55.14 seconds
2022-09-22 20:48:27,509 INFO    [ex1] Starting example/start
2022-09-22 20:48:27,915 INFO    [ex1] example/start completed in 0.41 seconds
2022-09-22 20:48:27,915 INFO    [env] Starting example/test
2022-09-22 20:48:51,202 INFO    [env] example/test completed in 23.29 seconds
2022-09-22 20:48:51,203 INFO    [env] Started in 78.83 seconds
```

We have 2 minikube profiles:

```
$ minikube profile list
|---------|-----------|------------|----------------|------|---------|---------|-------|--------|
| Profile | VM Driver |  Runtime   |       IP       | Port | Version | Status  | Nodes | Active |
|---------|-----------|------------|----------------|------|---------|---------|-------|--------|
| ex1     | kvm2      | containerd | 192.168.50.136 | 8443 | v1.24.3 | Running |     1 |        |
| ex2     | kvm2      | containerd | 192.168.39.240 | 8443 | v1.24.3 | Running |     1 |        |
|---------|-----------|------------|----------------|------|---------|---------|-------|--------|
```

ngix was deployed on both clusters:

```
$ minikube kubectl -p ex1 -- get pods
NAME                                READY   STATUS    RESTARTS   AGE
nginx-deployment-6595874d85-sr5nt   1/1     Running   0          3m32s

$ minikube kubectl -p ex2 -- get pods
NAME                                READY   STATUS    RESTARTS   AGE
nginx-deployment-6595874d85-bq7lq   1/1     Running   0          4m19s
```

After starting both clusters, we run the `example/test` script. We can
run the script manually again to test the clusters:

```
$ example/test ex1 ex2
* Testing example deploymnet on cluster ex1
  deployment "nginx-deployment" successfully rolled out
* Testing example deploymnet on cluster ex2
  deployment "nginx-deployment" successfully rolled out
```

If something failed while starting, we can run start again. This is
typically much faster:

```
$ drenv start example.yaml
2022-09-22 20:51:41,309 INFO    [env] Using example.yaml
2022-09-22 20:51:41,311 INFO    [ex1] Starting cluster
2022-09-22 20:51:41,311 INFO    [ex2] Starting cluster
2022-09-22 20:51:55,456 INFO    [ex2] Cluster started in 14.14 seconds
2022-09-22 20:51:55,456 INFO    [ex2] Starting example/start
2022-09-22 20:51:55,671 INFO    [ex1] Cluster started in 14.36 seconds
2022-09-22 20:51:55,671 INFO    [ex1] Starting example/start
2022-09-22 20:51:55,678 INFO    [ex2] example/start completed in 0.22 seconds
2022-09-22 20:51:55,868 INFO    [ex1] example/start completed in 0.20 seconds
2022-09-22 20:51:55,868 INFO    [env] Starting example/test
2022-09-22 20:51:56,089 INFO    [env] example/test completed in 0.22 seconds
2022-09-22 20:51:56,089 INFO    [env] Started in 14.78 seconds
```

While debugging it is useful to use the `--verbose` option to see much
more details from scripts:

```
$ drenv start example.yaml -v
2022-09-22 20:52:20,518 INFO    [env] Using example.yaml
2022-09-22 20:52:20,520 INFO    [ex1] Starting cluster
2022-09-22 20:52:20,520 INFO    [ex2] Starting cluster
2022-09-22 20:52:20,573 DEBUG   [ex1] * [ex1] minikube v1.26.1 on Fedora 36
2022-09-22 20:52:20,573 DEBUG   [ex2] * [ex2] minikube v1.26.1 on Fedora 36
2022-09-22 20:52:20,574 DEBUG   [ex2]   - MINIKUBE_HOME=/data/minikube
2022-09-22 20:52:20,574 DEBUG   [ex1]   - MINIKUBE_HOME=/data/minikube
2022-09-22 20:52:21,073 DEBUG   [ex2] * Using the kvm2 driver based on existing profile
2022-09-22 20:52:21,082 DEBUG   [ex1] * Using the kvm2 driver based on existing profile
2022-09-22 20:52:21,091 DEBUG   [ex2] * Starting control plane node ex2 in cluster ex2
2022-09-22 20:52:21,109 DEBUG   [ex1] * Starting control plane node ex1 in cluster ex1
2022-09-22 20:52:21,159 DEBUG   [ex2] * Updating the running kvm2 "ex2" VM ...
2022-09-22 20:52:22,542 DEBUG   [ex1] * Updating the running kvm2 "ex1" VM ...
2022-09-22 20:52:26,852 DEBUG   [ex2] * Preparing Kubernetes v1.24.3 on containerd 1.6.6 ...
2022-09-22 20:52:27,986 DEBUG   [ex1] * Preparing Kubernetes v1.24.3 on containerd 1.6.6 ...
2022-09-22 20:52:37,553 DEBUG   [ex2] * Configuring bridge CNI (Container Networking Interface) ...
2022-09-22 20:52:37,811 DEBUG   [ex2] * Verifying Kubernetes components...
2022-09-22 20:52:37,889 DEBUG   [ex2]   - Using image gcr.io/k8s-minikube/storage-provisioner:v5
2022-09-22 20:52:38,661 DEBUG   [ex2] * Enabled addons: storage-provisioner, default-storageclass
2022-09-22 20:52:38,664 DEBUG   [ex2] * kubectl not found. If you need it, try: 'minikube kubectl -- get pods -A'
2022-09-22 20:52:38,665 DEBUG   [ex2] * Done! kubectl is now configured to use "ex2" cluster and "default" namespace by default
2022-09-22 20:52:38,687 INFO    [ex2] Cluster started in 18.17 seconds
2022-09-22 20:52:38,687 INFO    [ex2] Starting example/start
2022-09-22 20:52:38,715 DEBUG   [ex2] * Deploying nginx on cluster ex2
2022-09-22 20:52:38,786 DEBUG   [ex1] * Configuring bridge CNI (Container Networking Interface) ...
2022-09-22 20:52:38,888 DEBUG   [ex2]   deployment.apps/nginx-deployment unchanged
2022-09-22 20:52:38,892 INFO    [ex2] example/start completed in 0.20 seconds
2022-09-22 20:52:38,999 DEBUG   [ex1] * Verifying Kubernetes components...
2022-09-22 20:52:39,073 DEBUG   [ex1]   - Using image gcr.io/k8s-minikube/storage-provisioner:v5
2022-09-22 20:52:39,990 DEBUG   [ex1] * Enabled addons: storage-provisioner, default-storageclass
2022-09-22 20:52:39,992 DEBUG   [ex1] * kubectl not found. If you need it, try: 'minikube kubectl -- get pods -A'
2022-09-22 20:52:39,994 DEBUG   [ex1] * Done! kubectl is now configured to use "ex1" cluster and "default" namespace by default
2022-09-22 20:52:40,014 INFO    [ex1] Cluster started in 19.49 seconds
2022-09-22 20:52:40,014 INFO    [ex1] Starting example/start
2022-09-22 20:52:40,041 DEBUG   [ex1] * Deploying nginx on cluster ex1
2022-09-22 20:52:40,207 DEBUG   [ex1]   deployment.apps/nginx-deployment unchanged
2022-09-22 20:52:40,211 INFO    [ex1] example/start completed in 0.20 seconds
2022-09-22 20:52:40,211 INFO    [env] Starting example/test
2022-09-22 20:52:40,234 DEBUG   [env] * Testing example deploymnet on cluster ex1
2022-09-22 20:52:40,339 DEBUG   [env]   deployment "nginx-deployment" successfully rolled out
2022-09-22 20:52:40,339 DEBUG   [env] * Testing example deploymnet on cluster ex2
2022-09-22 20:52:40,444 DEBUG   [env]   deployment "nginx-deployment" successfully rolled out
2022-09-22 20:52:40,448 INFO    [env] example/test completed in 0.24 seconds
2022-09-22 20:52:40,448 INFO    [env] Started in 19.93 seconds
```

We can stop the environment and start it later. This can be faster than
recreating it from scratch, but tends to be flaky.

To delete the environment:

```
$ drenv delete example.yaml
2022-09-22 20:54:05,106 INFO    [env] Using example.yaml
2022-09-22 20:54:05,108 INFO    [ex1] Deleting cluster
2022-09-22 20:54:05,109 INFO    [ex2] Deleting cluster
2022-09-22 20:54:05,863 INFO    [ex2] Cluster deleted in 0.75 seconds
2022-09-22 20:54:05,906 INFO    [ex1] Cluster deleted in 0.80 seconds
2022-09-22 20:54:05,906 INFO    [env] Deleted in 0.80 seconds
```

### The environment file format

- `profiles`: list of profiles.
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
    - `scripts`: Optional list of scripts to run during start.
        - `file`: Script filename
        - `args`: Optional argument to the script. If not specified the
          script is run without any arguments.
- `scripts`: Optional list of scripts to run after the profile scripts.
    - `file`: Script filename
    - `args`: Optional argument to the script. If not specified the
      script is run without any arguments.
