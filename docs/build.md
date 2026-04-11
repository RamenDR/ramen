<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Building ramen

## Go toolchain

*Ramen* uses Go's [toolchain management](https://go.dev/doc/toolchain)
to specify both the minimum required Go version and the recommended
toolchain version in `go.mod`:

- The `go` directive specifies the minimum Go version required to build
  *Ramen*.
- The `toolchain` directive specifies the recommended Go toolchain for
  development. By default, Go automatically downloads this version when
  a newer toolchain is needed.

### Building with an older Go toolchain

If you are building in a disconnected environment or with a Go version
older than the `toolchain` directive, set `GOTOOLCHAIN` to use the
locally installed Go toolchain:

```sh
export GOTOOLCHAIN=local
```

This works as long as the local Go version satisfies the `go` minimum
version in `go.mod`. For more details see
[Go Toolchain](https://go.dev/doc/toolchain#select).

## Container image

The Dockerfile uses the official `golang` base image which sets
`GOTOOLCHAIN=local`, so the build uses the Go version from the base
image without downloading a different toolchain.

To build with a different Go version, use the `GO_VERSION` build
argument:

```sh
podman build --build-arg GO_VERSION=1.25 -t ${IMG} .
```
