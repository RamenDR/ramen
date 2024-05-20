# Handling CVEs in the project

## Introduction

CVE, short for Common Vulnerabilities and Exposures, is a list of publicly
disclosed computer security flaws. When someone refers to a CVE, they mean a
security flaw that's been assigned a CVE ID number.

## CVEs filed on dependencies

### Checking if a CVE applies to Ramen

**Note**: Ramen has two go modules, one for the main project and another for
the api. You need to perform the check below for both the modules.

When a CVE is filed on a direct or an indirect dependency of Ramen, you will
first have to check if it applies to a particular branch. Some of the CVE
analysis tools make use of the go.sum file to make this determination and there
might be false positives in those methods.

To check whether a package is really used in Ramen or not you can use the
following command.

```
go mod why -m <module path>
```

This command shows only the shortest path to the module and there could be more
usage of it elsewhere.

Here is an example of the command for three different scenarios

1. When the module is used by Ramen directly. The output shows that the
   controllers package makes use of the time module and specifically the rate
   package in it.

    ```
    $ go mod why -m golang.org/x/time
    # golang.org/x/time
    github.com/ramendr/ramen/internal/controller
    golang.org/x/time/rate
    ```

1. When the module is an indirect dependency

    ```
    $ go mod why -m github.com/go-logr/zapr
    # github.com/go-logr/zapr
    github.com/ramendr/ramen
    sigs.k8s.io/controller-runtime/pkg/log/zap
    github.com/go-logr/zapr
    ```

1. When a module isn't used by Ramen directly or indirectly. If a module isn't
   used by Ramen then you can ignore the CVE.

    ```
    $ go mod why -m github.com/open-telemetry/opentelemetry-go-contrib
    # github.com/open-telemetry/opentelemetry-go-contrib
    (main module does not need module github.com/open-telemetry/opentelemetry-go-contrib)
    ```

## Fixing the CVEs by updating the packages

1. When a module is a direct dependency, then using `go get` to update the
   version of the module to the one that fixes the CVE is sufficient.

1. When a module is an indirect dependency, you will have to determine if the
   module which brings in the transitive dependency has a fix for the CVE. If
   it does, you can update the version of the module to the one that fixes the
   CVE using `go get`. If it doesn't, you will have to wait for the module to
   update the version of the transitive dependency or you can use a replace
   directive in the go.mod file to update the version of the transitive
   dependency.
