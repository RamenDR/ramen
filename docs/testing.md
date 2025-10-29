<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Testing

## Unit tests and Integration tests

The unit and integration tests for the controller code are written using the
`ginkgo` and `gomega` testing frameworks. The tests are written in the
`*_test.go` files in the `controllers` directory.

By default, the tests are run against a single kubernetes api
server(<https://github.com/kubernetes-sigs/controller-runtime/tree/main/tools/setup-envtest>)
provided by the controller-runtime project.

The test framework uses mock interfaces for the components that aren't installed
in the test environment, like ocm, s3 and csi. These mock interfaces are used to
fake a success or failure from the components to test the controller code.

```sh
$ make test
hack/install-setup-envtest.sh
go test ./... -coverprofile cover.out
        github.com/ramendr/ramen/cmd    [no test files]
        github.com/ramendr/ramen/internal/controller/argocd [no test files]
        github.com/ramendr/ramen/internal/controller/kubeobjects    [no test files]
        github.com/ramendr/ramen/internal/controller/kubeobjects/velero [no test files]
ok      github.com/ramendr/ramen/internal/controller    72.451s coverage: 67.6% of statements
ok      github.com/ramendr/ramen/internal/controller/cel    6.190s  coverage:   [no statements]
ok      github.com/ramendr/ramen/internal/controller/util   6.387s  coverage: 19.9% of statements
ok      github.com/ramendr/ramen/internal/controller/volsync    19.654s coverage: 57.6% of statements
```

Explore other `test-` targets that test a subset of the controller code.

### Coverage

The tests are run with coverage enabled. You can see the coverage report by
running the coverage target.

To open an HTML coverage report in the default browser run:

```sh
make coverage
```

The coverage report depends on the tests ran before inspecting the
coverage.

### Using interfaces to mock in testing

It is always useful to run the unit or integration tests without setting up
external dependencies. If the communication to the external components is done
through interfaces, it becomes easy to mock those components by using a fake or
mock implementation of those dependencies which satisfy those interfaces in the
tests.

![](interfaces.png?raw=true)

The above picture shows the interfaces that are used in Ramen today.

## End-to-end tests

The end-to-end testing framework provides comprehensive testing capabilities
for various DR scenarios using predefined workloads and deployment methods.

Please refer to the [e2e testing documentation](e2e.md) for detailed
instructions on:

- Setting up the test environment
- Running validation tests
- Executing specific DR test scenarios
- Understanding test results and debugging failures

For step-by-step manual testing and debugging, see the
[Step-by-step DR workflow testing](e2e.md#step-by-step-dr-workflow-testing)
section which provides individual control over each DR operation.

The e2e framework validates operations including:

1. Deploying applications with persistent volumes
1. Enabling DR for applications
1. Failing over applications to other clusters
1. Relocating applications back to original clusters
1. Disabling DR for applications
1. Undeploying applications

The framework supports multiple deployers (appset, subscr, disapp) and
storage types (rbd, cephfs) with comprehensive validation and reporting.

For more info on writing such tests see
[test/README.md](../test/README.md).
