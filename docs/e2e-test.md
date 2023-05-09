# Ramen End-to-End (E2E) Test Framework

This document will provide an introduction to the framework, describe its components and structure, and explain how to run the tests.

## Overview

The E2E test framework is designed to validate the functionality of the Ramen project from a user's perspective. This means that it will test the entire system. The framework is built using Go and follows the native Go test format. Setting up the test environment is outside the scope of the framework, and is left to the user. The framework will however, detect the environment and run the tests accordingly.

## Structure

### Go workspaces

The E2E test framework uses Go workspaces to separate the test dependencies from the Ramen dependencies. This is done to avoid any conflicts between the two. The Go workspaces are defined in the go.work file, and the checksums for the dependencies are defined in the go.work.sum file. Go workspaces provides us the following benefits:

* Isolation of Test Dependencies: By using a Go.work file, we can maintain a separate list of dependencies for our tests. This approach keeps our main project's dependencies uncluttered, reducing the risk of conflicts and making it easier to manage dependencies.
* Improved Reproducibility: A Go.work file allows us to explicitly define the versions of dependencies required for our tests. This ensures that all team members work with the same dependency versions, leading to consistent test results and reducing the likelihood of hard-to-debug issues stemming from dependency mismatches.
* Simplified Dependency Updates: When updating dependencies, a Go.work file enables us to quickly identify and update test-specific dependencies without affecting the main project. This reduces the chances of inadvertently introducing breaking changes or compatibility issues.
* Easier Onboarding: For new team members or external contributors, a Go.work file provides a clear separation of test dependencies from the main project. This simplifies the onboarding process and makes it easier for newcomers to understand the project structure and contribute effectively.
* Enhanced Security: By isolating test dependencies, we minimize the potential attack surface in our main project. This approach helps ensure that vulnerabilities in test dependencies do not compromise the security of our production code.

You can read more about go.work files here. https://go.dev/ref/mod#workspaces

### Configuration


The E2E test framework is organized into the following directories and files:

* e2e/: The main directory containing the E2E test framework.
* e2e/config/: The directory containing the configuration files for the E2E tests.
* e2e/config/config.yaml: The main configuration file for the E2E tests.
* e2e/go.mod: The Go module file containing the E2E test dependencies.
* e2e/go.sum: The Go checksum file containing the E2E test dependencies' checksums.
* e2e/main_test.go: The main test file where the test suites are defined.

The E2E test framework utilizes [Viper](https://github.com/spf13/viper) for loading and validating the configuration, and [Testify](https://github.com/stretchr/testify) for running test suites and performing validations.

## Running the Tests

## Running the Entire E2E Test
To run the entire E2E test suite, use the following make target:

```sh
make test-e2e
```
