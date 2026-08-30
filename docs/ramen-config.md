<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Operator RamenConfig: bootstrap and merge

This document describes how the Ramen manager builds its initial
`RamenConfig`, ensures the operator ConfigMap exists in the cluster,
and merges in-process defaults with user-supplied YAML. The flow is
implemented in `cmd/main.go` and `internal/controller/ramenconfig.go`;
YAML merging is done by `internal/config.Merge`.

## 1. Terms

**In-process defaults** means the `*ramendrv1alpha1.RamenConfig` from
`DefaultRamenConfig` after startup validation: the current binary's
default field values (leader election name, metrics, VolSync, etc.).

**System YAML** is `yaml.Marshal` of that struct; it is the first
argument to `config.Merge`.

**User YAML** is the string **value** of the `ConfigMap.data` entry
whose **key** is `ramen_manager_config.yaml` (`ConfigMapRamenConfigKeyName`).
That string is the YAML document; it is the second argument to `Merge`.

**Controller type** is `dr-hub` or `dr-cluster`. It is read from the
**environment variable** `RAMEN_CONTROLLER_TYPE` (for example set on the
operator Deployment or Subscription). That value must be **preset when
the operator is installed**—there is no default in code. It is stored
in `ControllerType`. The same value drives **which reconcilers are
started**: `setupReconcilers` in `cmd/main.go` calls
`setupReconcilersHub` for hub and `setupReconcilersCluster` for
dr-cluster, so the two deployments run **different controller sets**
(not only different defaults or ConfigMap names). It also selects the
operator ConfigMap name and default leader-election id.

## 2. Startup

The following happens before reconcilers run.

`main` parses flags and builds logger options. `buildOptions` calls
`LoadControllerConfig`, which requires `RAMEN_CONTROLLER_TYPE`, sets
`ControllerType`, and returns `DefaultRamenConfig(ct)`.
`LoadControllerOptions` applies that struct to controller-runtime
options. `configureController` registers schemes for hub or
dr-cluster. `newManager` creates the manager. Then
`CreateOrUpdateConfigMap` runs and may create or update the ConfigMap;
its returned `*RamenConfig` is passed to `setupReconcilers`, which wires
only the hub or only the dr-cluster reconcilers according to
`ControllerType`.

Defaults used at merge time are always the current code's
`DefaultRamenConfig` for the running controller type.

### Which ConfigMap

The object lives in `POD_NAMESPACE` (`RamenOperatorNamespace()`).
The name comes from `ramenOperatorConfigMapName()`: hub uses
`ramen-hub-operator-config`, dr-cluster uses
`ramen-dr-cluster-operator-config`. The operator reads and writes one
YAML document in `ConfigMap.data`: the **value** for the key
`ramen_manager_config.yaml` (same name as in install manifests; it is a
map key, not a path on disk).

## 3. No user ConfigMap yet

If `Get` returns `NotFound`, `CreateOrUpdateConfigMap` calls
`configMapCreate`. `ConfigMapNew` marshals `defaultRamenConfig` to YAML
and puts it in `data[ramen_manager_config.yaml]`; the ConfigMap is
created. There
is no merge on this path: the cluster gets an exact copy of the
in-process defaults as YAML.

## 4. Existing ConfigMap

If the ConfigMap exists, `defaultYAML` is `yaml.Marshal` of the
in-process defaults, and `userYAML` is the bytes of
`data[ramen_manager_config.yaml]` (the stored YAML string).
`rameninternalconfig.Merge(defaultYAML, userYAML)` builds the merged
struct; `configMapUpdate` marshals it and updates the ConfigMap. On
every startup with an existing object, the stored YAML is refreshed to
defaults plus user overrides.

### Merge behavior

`Merge` unmarshals system YAML into a `RamenConfig`, then unmarshals
user YAML into the same value (user keys overwrite).

If a **new default** appears only in the new binary's defaults and the
user YAML **does not** list that field, the merged result still
contains that new default and it is written back.

Fields **only in user YAML** stay. Where **both** set a field, **user**
wins.

This is merge-by-unmarshal into the Go struct; unknown YAML keys are
not preserved.

### Summary

| Situation        | Merge | Written YAML                               |
| ---------------- | ----- | ------------------------------------------ |
| No ConfigMap     | No    | In-process `DefaultRamenConfig`            |
| ConfigMap exists | Yes   | `Merge(marshal(defaults), user bytes)`     |

## 5. Notes

`RAMEN_CONTROLLER_TYPE` is read from the **environment** at process
start. It must be set **before** the operator runs (typically when the
hub or dr-cluster install is applied). If it is missing or not
`dr-hub` / `dr-cluster`, startup fails before ConfigMap logic.
Reconcilers use the `*RamenConfig` returned from
`CreateOrUpdateConfigMap`. Other code paths (for example S3 profiles)
may use `ConfigMapGet` to load that ConfigMap and unmarshal the
`data[ramen_manager_config.yaml]` string.
