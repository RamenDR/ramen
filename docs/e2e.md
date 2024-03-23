# End-to-End (E2E) Testing Framework Documentation

The E2E testing framework is designed to facilitate comprehensive integration
testing of the Ramen project across multiple Kubernetes clusters. This document
provides an overview of the framework's setup, usage and architecture.

## Setup and Usage

The framework is designed to be run as a standalone program and needs a config
file in the same directory as the binary. The config file should be named
`config.yaml` and should have the following structure:

```yaml
clusters:
  "hub":
    kubeconfigpath: "/path/to/hub/kubeconfig"
  "c1":
    kubeconfigpath: "/path/to/c1/kubeconfig"
  "c2":
    kubeconfigpath: "/path/to/c2/kubeconfig"
```

### Build

To build the binary, run the following command:

```bash make build-e2e ```

It will create a binary named `ramen_e2e` in the e2e directory.  Create a
`config.yaml` file in the same directory as the binary and run the binary.

### Running default suites

```bash cd e2e ./ramen_e2e run-default-suites ```

### Running selected suites

```bash cd e2e ./ramen_e2e run-enabled-suites --validate-environment=true ```

### Listing available suites

```bash cd e2e ./ramen_e2e list-suites ```

## Architecture

The framework initializes by reading the configuration, setting up the
TestContext, and preparing the logger. Tests are executed by invoking the
`run-` subcommands, which read the configuration, initialize the test context,
and run the enabled test suites. Each suite runs its setup routine, executes
all tests, and finally runs the teardown routine.
