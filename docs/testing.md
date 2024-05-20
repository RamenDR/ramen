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

The end-to-end testing framework isn't implemented yet. However, we have a basic
test that you can use to test the basic flows of Ramen. `basic-test` requires
the python virtual environment to be activated.

Ramen basic test use the [ocm-ramen-samples repo](https://github.com/RamenDR/ocm-ramen-samples).
Before running the tests, you need to deploy a channel pointing this
repo:

```sh
kubectl apply -k https://github.com/RamenDR/ocm-ramen-samples.git/channel?ref=main --context hub
```

> [!NOTE]
> To test applications from your repo, you need to deploy a channel
> pointing to your repo.

To run basic tests using regional-dr environment run:

```sh
test/basic-test/run test/envs/regional-dr.yaml
```

This test does these operations:

1. Deploys a busybox application
1. Enables DR for the application
1. Fails over the application to the other cluster
1. Relocates the application back to the original cluster
1. Disables DR for the application
1. Undeploys the application

If needed, you can run one or more steps form this test, for example to
deploy and enable DR run:

```sh
env=$PWD/test/envs/regional-dr.yaml
test/basic-test/deploy $env
test/basic-test/enable-dr $env
```

At this point you can run run manually failover, relocate one or more
times as needed:

```sh
for i in $(seq 3); do
    test/basic-test/relocate $env
done
```

To clean up run:

```sh
test/basic-test/undeploy $env
```

For more info on writing such tests see
[test/README.md](../test/README.md).
