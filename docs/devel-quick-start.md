<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Ramen developer quick start guide

## Contribution Flow

1. Fork the repository on GitHub
1. Create a branch from where you want to base your work (usually main).
1. Make your changes and arrange them in readable commits.
1. Make sure your commit messages are in the proper format.
1. Make sure all [tests](./testing.md) pass, and add any new [tests](./testing.md)
1. Push your changes to the branch in your fork of the repository.
   as appropriate.
1. Submit a pull request to the original repository.

## Getting the source

1. Fork the *Ramen* repository on github

1. Clone the source locally

   ```sh
   git clone git@github.com:my-github-user/ramen.git
   ```

1. Add an `upstream` remote, to make it easy to sync your repo from
   upstream.

   ```sh
   git remote add upstream https://github.com/RamenDR/ramen.git
   ```

1. Add the commit-msg hook to sign off your commits

   *Ramen* requires a `Signed-off-by: My Name <myemail@example.com>`
   footer in the commit message. To add it automatically to all commits,
   add this hook:

   ```sh
   cp hack/commit-msg .git/hooks/
   ```

## Setting up the environment for development and testing

To set up a *Ramen* development environment you will need to run 3
Kubernetes clusters. *Ramen* makes this easy using minikube, but you need
enough resources:

- Bare metal or virtual machine with nested virtualization enabled
- 8 CPUs or more
- 16 GiB of free memory
- 20 GiB of free disk space
- Internet connection
- Linux - tested on *RHEL* 8.6 and *Fedora* 37.

### Create and activate python virtual environment

The *Ramen* project uses python tools to create and provision test
environment and run tests. To create a virtual environment including
the tools run:

```sh
make venv
```

This creates a virtual environment in `~/.venv/ramen` and a symbolic
link `venv` for activating the environment.

Activate the python virtual environment:

```sh
source venv
```

### Create the environment

You can run the make target `create-rdr-env` to create a 3 cluster environment
for suitable for Regional DR. This will create 3 minikube clusters, and install
the necessary components for the Ramen operator to run.

This uses `drenv` tool to setup the environment. Refer to
[drenv readme](../test/README.md#setup) for more details.

```sh
make create-rdr-env
```

## Building the ramen operator image

Build the *Ramen* operator container image:

```sh
make docker-build
```

This builds the image `quay.io/ramendr/ramen-operator:latest`

## Deploying the ramen operator

You can either deploy the *Ramen* operator using the make targets or use the
`ramenctl` tool. For more info on the `ramenctl` tool see
[ramenctl/README.md](../ramenctl/README.md).

- Using `ramenctl`

    Ensure python virtual environment is active as `ramenctl` is a python tool.

    - Deploy the *Ramen* operator in the environment using `ramenctl`.

     ```sh
     ramenctl deploy test/envs/regional-dr.yaml
     ```

    - Ramen needs to be configured to know about the managed clusters and the s3
   endpoints. Configure the ramen operator for environment.

    ```sh
    ramenctl config test/envs/regional-dr.yaml
    ```

## Testing Ramen

- Read the [testing](./testing.md) guide for test instructions.

## Undeploying the ramen operator

If you want to clean up your environment, you can unconfigure *Ramen* and
undeploy it.

```sh
ramenctl unconfig test/envs/regional-dr.yaml
ramenctl undeploy test/envs/regional-dr.yaml
```
