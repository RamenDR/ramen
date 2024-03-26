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

```make build```

It will create a binary named `e2e` in the e2e directory.  Create a
`config.yaml` file in the same directory as the binary and run the binary.

### Running e2e tests

```./e2e or make run```
