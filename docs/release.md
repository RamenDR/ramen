<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Ramen Release Management

This page describes how the Ramen project maintainers release a version of Ramen.

## Prerequisites

- Write access to the GitHub repository
- Push access to quay.io/ramendr
- Container registry login:

  ```bash
  podman login quay.io
  ```

## Create New Branch

A new branch must be created for each release. The branch name should
follow the pattern `release-{number}`:

```bash
git checkout -b release-0.1.0
```

## Update Version

Update the `VERSION` variable in the Makefile:

```bash
VERSION ?= 0.1.0
```

Commit and push the version change:

```bash
git add Makefile
git commit -s -m "Release 0.1.0"
git push origin release-0.1.0
```

## Create and Push Tag

Create a git tag for the release:

```bash
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0
```

## Set Release Variables

Export all required variables for building and pushing images:

```bash
export VERSION=0.1.0
export IMAGE_TAG=v${VERSION}
export IMAGE_REGISTRY=quay.io
export IMAGE_REPOSITORY=ramendr
export IMG=${IMAGE_REGISTRY}/${IMAGE_REPOSITORY}/ramen-operator:${IMAGE_TAG}
export BUNDLE_IMG_HUB=${IMAGE_REGISTRY}/${IMAGE_REPOSITORY}/ramen-hub-operator-bundle:${IMAGE_TAG}
export BUNDLE_IMG_DRCLUSTER=${IMAGE_REGISTRY}/${IMAGE_REPOSITORY}/ramen-dr-cluster-operator-bundle:${IMAGE_TAG}
export CATALOG_IMG=${IMAGE_REGISTRY}/${IMAGE_REPOSITORY}/ramen-operator-catalog:${IMAGE_TAG}
```

## Build and Push Container Images

Build and push the operator container images:

```bash
make docker-build docker-push
```

## Generate and Push OLM Bundles

Generate the OLM bundles for hub and dr-cluster operators:

```bash
make bundle-hub
make bundle-dr-cluster

make bundle-hub-build
make bundle-hub-push

make bundle-dr-cluster-build
make bundle-dr-cluster-push
```

## Build and Push OLM Catalog

Build and push the OLM catalog image:

```bash
make catalog-build
make catalog-push
```

## Creating a GitHub Release

A new release will be created and updated with release notes.

Release notes can be generated using:

```bash
# List commits since last release
git log v0.0.1..HEAD --oneline
```

Create the release at <https://github.com/ramendr/ramen/releases/new>
with:

- Tag: `v0.1.0`
- Title: `Ramen v0.1.0`
- Description: Release notes with features, bug fixes, and known issues

## Updating the Community

An announcement should be made to the community with the new release and
changes in that version.
