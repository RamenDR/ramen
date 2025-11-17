<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Using Recipes for Disaster Recovery (DR)

## Introduction

A recipe is a Kubernetes Custom Resource that defines custom workflows for
capturing and recovering application resources and volumes during disaster
recovery (DR) operations. Recipes are part of Ramen's OpenShift-native Disaster
Recovery ecosystem, providing vendor-supplied or custom disaster recovery
specifications for complex stateful applications. While Ramen supports
multiple workload protection methods (GitOps, discovered applications),
Recipes are specifically designed for applications that require:

- Application-specific quiesce operations before capture
    (e.g., database flush)
- Strict resource ordering during capture or recovery
- Custom pre/post hooks for DR operations
- Vendor-defined DR workflows that ensure data consistency

**Target Audience:**

- **Software Vendors**: Create Recipes to accompany
    products, ensuring proper DR protection out-of-the-box.
- **Advanced Users**: Write custom Recipes when vendors
    haven't provided them for critical applications

**When to use Recipes:**

- **Complex stateful applications** (e.g., databases, middleware) requiring
 custom workflows
- **Applications needing quiesce operations** before capture (e.g., database
 flush, application freeze)
- **Strict resource ordering** required during capture/recovery (e.g.,
 ConfigMaps before Deployments)
- **Custom hooks needed** for pre/post DR operations
- **Vendor-provided Recipes** are available for your application
- **Standard volume replication alone** doesn't guarantee application
 consistency

**When NOT to use Recipes:**

- Simple stateless applications (use GitOps or discovered applications)
- Applications without specific capture/recovery requirements.
- When vendor-provided Recipes are not available and you lack expertise
 to create custom ones.

## Overview

A Recipe defines custom workflows for disaster recovery operations through
 four main components:

- **Groups**: Collections of Kubernetes resources with specific selection
 criteria.
- **Hooks**: Custom operations (exec, scale, check) executed during workflows.
 These are treated as pre/post operations for backup and restore.
- **Workflows**: Ordered sequences of groups and hooks for capture (backup)
 and recovery (restore).
- **Volumes**: Persistent volume selectors for data protection. If provided,
 will take highest precedence.

When a VolumeReplicationGroup (VRG) or DRPlacementControl (DRPC) references a
Recipe, Ramen executes the Recipe's workflows according to VRG's schedule.
The workflows define the exact order in which resources are captured or
recovered, ensuring application consistency.

Recipe source code and additional information can be found
[here](https://github.com/RamenDR/recipe).

By default, without Recipes, Kubernetes resources are captured and recovered as
single, namespaced groups without specific ordering. Recipes provide the
flexibility to define custom sequences tailored to application requirements.

**Note:** The terms "capture" and "recover" refer to the DR operations, while
Recipe workflows are named "backup" and "restore" respectively. Ramen
 automatically maps these operations to the corresponding workflows.

## Requirements

1. Recipe CRD must be installed on the cluster. The base Recipe CRD can be
 found [here](https://github.com/RamenDR/recipe/blob/main/config/crd/bases/ramendr.openshift.io_recipes.yaml).
1. Velero must be installed for Kubernetes object protection (see [install.md](install.md#velero-for-kube-object-protection))
1. The Recipe must exist in the namespace referenced by DRPC.

## Example

### Application overview

SampleApp has two specific requirements for Capture and Recover, shown below:

Capture:

1. ConfigMaps and Secrets must be captured before anything else
1. Deployments must be captured after these
1. All other resources can be captured after this

Recover:

1. ConfigMaps and Secrets must be recovered before anything else
1. Deployments must be recovered after these
1. All other resources can be restored after this

Volumes:
PVC volumes associated with the app should be protected, and are identified with
labelSelector `app=my-app`.

### Recipe sample

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: Recipe
metadata:
  name: sample-app-recipe
  namespace: my-app-ns
spec:
  appType: sample-app
  groups:
  - name: config
    type: resource
    includedResourceTypes:
    - configmap
    - secret
    includedNamespaces:
    - my-app-ns
    labelSelector:
      matchLabels:
        app: my-app
  - name: deployments
    type: resource
    includedResourceTypes:
    - deployment
    includedNamespaces:
    - my-app-ns
    labelSelector:
      matchLabels:
        app: my-app
  - name: other-resources
    type: resource
    excludedResourceTypes:
    - configmap
    - secret
    - deployment
    includedNamespaces:
    - my-app-ns
    labelSelector:
      matchLabels:
        app: my-app
  volumes:
    name: app-volumes
    type: volume
    labelSelector:
      matchLabels:
        app: my-app
  hooks:
  - name: scale-test
    nameSelector: busybox
    namespace: my-app-ns
    selectResource: deployment
    type: scale
  - name: exec-test
    nameSelector: busybox-.*
    namespace: my-app-ns
    selectResource: pod
    type: exec
    ops:
    - command: >
        ["/bin/sh", "-c", "ls"]
      name: busy
  - chks:
    - condition: '{$.spec.replicas} == {$.status.readyReplicas}'
      name: check-replicas
    name: backup
    nameSelector: busybox
    namespace: my-app-ns
    onError: fail
    selectResource: deployment
    timeout: 300
    type: check
  - chks:
    - condition: '{$.status.phase} == {Running}'
      name: check-replicas
    name: restore
    nameSelector: busybox-.*
    namespace: my-app-ns
    onError: fail
    selectResource: pod
    timeout: 300
    type: check
  workflows:
  - failOn: any-error  # Failure handling: any-error (default), essential-error, full-error
    name: backup
    sequence:
    - hook: backup/check-replicas
    - hook: scale-test/up
    - hook: scale-test/sync
    - hook: scale-test/down
    - group: config
    - group: deployments
    - group: other-resources
  - failOn: any-error  # Failure handling: any-error (default), essential-error, full-error
    name: restore
    sequence:
    - group: config
    - group: deployments
    - group: other-resources
    - hook: restore/check-replicas
    - hook: exec-test/busy
    - hook: restore/check-replicas
```

### VRG sample that uses this Recipe

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: VolumeReplicationGroup
metadata:
  name: vrg-sample
  namespace: my-app-ns # same Namespace as Recipe
spec:
  kubeObjectProtection:
    recipeRef:
      name: sample-app-recipe
      # namespace: my-app-ns  # Optional - only needed if Recipe is in different namespace
```

### Additional information about sample Recipe and VRG

There are several parts of this example to be aware of:

1. **Namespace requirement**: The VRG and Recipe must exist in the same namespace,
   unless the Recipe's namespace is explicitly specified in `recipeRef.namespace`.
   If `recipeRef.namespace` is omitted, Ramen assumes the Recipe is in the same
   namespace as the VRG.

1. **Workflow names**: Recipe workflows must be named "backup" and "restore" (not
   "capture" and "recover"). Ramen automatically discovers these workflows by name.

1. **Recipe updates**: Once the VRG references a Recipe, users only need to update
   the Recipe itself to change the capture/recover order or resource types. The VRG
   will automatically use the updated Recipe.

1. **VRG configuration**: Capture and recover actions are performed on the VRG, not
   by the Recipe. The VRG controls the scheduling interval, S3 profile information,
   and sync/async configuration.

1. **Group reuse**: Groups can be referenced by arbitrary sequences. If they apply
   to both a backup workflow and a restore workflow, the group may be reused.

1. **Volumes**: The volumes section in the Recipe automatically determines which
   PVCs are protected. No additional configuration is needed in the VRG.

1. **Hook prerequisites**: In order to run hooks, the relevant Pods and containers
   must be running before the hook is executed. This is the responsibility of the
   user and application, and Recipes do not check for valid Pods prior to running
   a workflow.

1. **Hook execution**: Hooks may use arbitrary commands, but they must be able to
   run on a valid container found within the app. Hooks can target resources using
   `nameSelector` (regex pattern) or `labelSelector`. By default, hooks will
   attempt to run on all matching Pods and search for the specified container.
   Use `labelSelector` to limit where hooks can run if needed.

## Additional usage examples

For more comprehensive examples, advanced use cases, and detailed Recipe specifications,
see the [Recipe repository README](https://github.com/RamenDR/recipe/blob/main/README.md).
The repository contains additional examples, best practices, and detailed documentation
on creating and managing Recipes for various application types.
