<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Ramen developer quick start guide

This page will help you to set up a development environment for the
*Ramen* project.

## What you’ll need

To set up a *Ramen* development environment you will need to run 3
Kubernetes clusters. *Ramen* makes this easy using minikube, but you need
enough resources:

- Bare metal or virtual machine with nested virtualization enabled
- 8 CPUs or more
- 16 GiB of free memory
- 20 GiB of free disk space
- Internet connection
- Linux - tested on *RHEL* 8.6 and *Fedora* 37.

## Getting the source

1. Fork the *Ramen* repository on github

1. Clone the source locally

   ```
   git clone git@github.com:my-github-user/ramen.git
   ```

1. Add an `upstream` remote, to make it easy to sync your repo from
   upstream.

   ```
   git remote add upstream https://github.com/RamenDR/ramen.git
   ```

1. Set up a commit-msg hook to sign off your commits

   *Ramen* requires a `Signed-off-by: My Name <myemail@example.com>`
   footer in the commit message. To add it automatically to all commits,
   add this hook:

   ```
   $ cat .git/hooks/commit-msg
   #!/bin/sh
   # Add Signed-off-by trailer.
   sob=$(git var GIT_AUTHOR_IDENT | sed -n 's/^\(.*>\).*$/Signed-off-by: \1/p')
   git interpret-trailers --in-place --trailer "$sob" "$1"
   ```

   And make the hook executable:

   ```
   chmod +x .git/hooks/commit-msg
   ```

1. Create python virtual environment

   The *Ramen* project use python tool to create and provision test
   environment and run tests. The create a virtual environment including
   the tools run:

   ```
   make venv
   ```

   This create a virtual environment in `~/.venv/ramen` and a symbolic
   link `venv` for activating the environment. To activate the
   environment use:

That's all! You are ready to submit your first pull request!

## Running the tests

To run all unit tests run:

```
make test
```

To run only part of the test suite you can use one of the `test-*`
targets, for example:

```
make test-vrg-vr
```

To open an HTML coverage report in the default browser run:

```
make coverage
```

The coverage report depends on the tests ran before inspecting the
coverage.

## Setting up the `drenv` tool

*Ramen* uses the `drenv` tool to build a development environment
quickly. Please follow the
[Setup instructions](../test/README.md#setup)
to set up the `drenv` tool.

## Starting the test environment

Before using the `drenv` tool to start a test environment, you need to
activate the python virtual environment:

```
source venv
```

*Ramen* supports many configurations, but the only development
environment available now is the `regional-dr.yaml`. This environment
simulates a Regional DR setup, when the managed clusters are located in
different zones (e.g. US, Europe) and storage is replicated
asynchronously. This environment includes 3 clusters - a hub cluster
(`hub`), and 2 managed clusters (`dr1`, `dr2`) using `Ceph` storage.

To start the environment, run:

```
cd test
drenv start envs/regional-dr.yaml
```

Starting takes at least 5 minutes, depending on your machine and
internet connection.

You can inspect the environment using kubectl, for example:

```
kubectl get deploy -A --context hub
kubectl get deploy -A --context dr1
kubectl get deploy -A --context dr2
```

## Building the ramen operator image

Build the *Ramen* operator container image:

```
make docker-build
```

This builds the image `quay.io/ramendr/ramen-operator:latest`

## Deploying the ramen operator

To deploy the *Ramen* operator in the test environment:

```
ramenctl deploy test/envs/regional-dr.yaml
```

## Configure the ramen operator on the hub

Ramen need to be configured to know about the managed clusters and the
s3 endpoints. To configure the ramen operator for test environment:

```
ramenctl config test/envs/regional-dr.yaml
```

For more info on the `ramenctl` tool see
[ramenctl/README.md](../ramenctl/README.md).

## Running system tests

At this point *Ramen* is ready to protect workloads in your cluster, and
you are ready for testing basic flows.

To run basic tests using regional-dr environment run:

```
test/basic-test/run test/envs/regional-dr.yaml
```

This test:

1. Deploys an application using
   [ocm-ramen-samples repo](https://github.com/RamenDR/ocm-ramen-samples)
1. Enables DR for the application
1. Fails over the application to the other cluster
1. Relocates the application back to the original cluster
1. Disables DR for the application
1. Undeploys the application

If needed, you can run one or more steps form this test, for example to
deploy and enable DR run:

```
env=$PWD/test/envs/regional-dr.yaml
test/basic-test/deploy $env
test/basic-test/enable-dr $env
```

At this point you can run run manually failover, relocate one or more
times as needed:

```
for i in $(seq 3); do
    test/basic-test/relocate $env
done
```

To clean up run:

```
test/basic-test/undeploy $env
```

For more info on writing such tests see
[test/README.md](../test/README.md).

## Undeploying the ramen operator

If you want to clean up your environment, you can unconfigure *Ramen* and
undeploy it.

```
ramenctl unconfig test/envs/regional-dr.yaml
ramenctl undeploy test/envs/regional-dr.yaml
```
