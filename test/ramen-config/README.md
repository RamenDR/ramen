<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Ramen configuration for test environment

This directory configures *Ramen* for drenv based test environment.

## Assumptions

- You have 3 clusters "hub", "dr1", and "dr2"
- The cluster "hub" is the hub cluster
- Clusters "dr1" and "dr2" are managed clusters
- *Ramen* was deployed using `make deploy-hub`

## Configuring

```
ramen-config/deploy regional-dr
```

## Removing the configuration

```
ramen-config/undeploy regional-dr
```

Notes:

- The ramen configmap is not removed. If you want to restore it to
  default value, run `make deploy-hub` again.
