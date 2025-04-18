<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Kubernetes Resource Protection with Recipes

## Overview

By default, Kubernetes resources are Captured and Recovered as single, namespaced
groups. Some applications may require strict ordering by type of resource during
Capture or Recover to ensure a successful deployment. To provide this flexibility,
Recipes can be used to describe a Workflow used for a Capture or Recover action.
These Recipe Workflows can be referenced by the VolumeReplicationGroup (VRG), and
executed periodically.

Recipe source code and additional information can be found [here](https://github.com/RamenDR/recipe).

## Requirements

1. Recipe CRD must be available on the cluster. The base Recipe CRD can be found
  [here](https://github.com/RamenDR/recipe/blob/main/config/crd/bases/ramendr.openshift.io_recipes.yaml)
1. The Recipe target must be available in the same Namespace as the application.

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
LabelSelector `app=my-app`.

### Recipe sample

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: Recipe
metadata:
  name: recipe-sample
  namespace: my-app-ns
spec:
  appType: demo-app  # required, but not currently used
  volumes:
    type: volume
    name: my-vol
    includedNamespaces:
    - cephfs-volume
    labelSelector:
      matchExpressions:
        - key: app
          operator: In
          values:
          - my-app
  groups:
  - includedNamespaces:
    - my-app-ns
    name: rg1
    type: resource
  - includedResourceTypes:
    - configmaps
    - secrets
  - includedNamespaces:
    - my-app-ns
    name: rg2
    type: resource
  - includedResourceTypes:
    - deployments
  hooks:
  - chks:
    - condition: '{$.spec.replicas} == {$.status.readyReplicas}'
      name: check-replicas
    name: backup
    nameSelector: busybox
    namespace: my-app-ns
    selectResource: deployment
    type: check
    - condition: '{$.spec.replicas} == 1'
      name: check-replicas
    name: restore
    nameSelector: busybox
    namespace: my-app-ns
    selectResource: deployment
    type: check
  workflows:
  - name: backup  # referenced in VRG
    sequence:
    - hook: backup/check-replicas
    - group: rg1
    - group: rg2
  - name: recover  # referenced in VRG
    sequence:
    - group: rg1
    - group: rg2
    - hook: restore/check-replicas
```

### VRG sample that uses this Recipe

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: VolumeReplicationGroup
metadata:
  name: vrg-sample
  namespace: my-app-ns  # same Namespace as Recipe
spec:
  kubeObjectProtection:
    recipeRef:
      name: recipe-sample
      namespace: my-app-ns
```

### Additional information about sample Recipe and VRG

There are several parts of this example to be aware of:

1. Both the VRG and Recipe may exist in the same Namespace or in different
   namespace with proper reference to the recipe in the vrg.
1. Once the VRG has the Recipe information on it, users only need to update the
   Recipe itself to change the Capture/Protect/Backup order or types.
1. Capture/Backup and Recover/Restore actions are performed on the VRG, not by
   the Recipe. This includes the scheduling interval, s3 profile information,
   and sync/async configuration.
1. Groups can be referenced by arbitrary sequences. If they apply to both a
   Capture/Backup Workflow and a Recover/Restore Workflow, the group may be
   reused.
1. In order to run Hooks, the relevant Pods and containers must be running
   before the Hook is executed. This is the responsibility of the user and
   application, and Recipes do not check for valid Pods prior to running a
   Workflow.
1. Hooks can be of the following types:
   a. Check hooks -- These can be used to assess the health of the system
      before taking backup or after restore.
   b. Exec hooks -- These can be used to execute some scripts or commands
      on selected pods in order change the state of the system.
1. Exec hooks may use arbitrary commands, but they must be able to run on
   a valid container found within the app. For example, a Pod with container
   `main` has scripts appropriate for running Hooks. Be aware that by default,
   Hooks will attempt to run on all valid Pods and search for the specified
   container. If several Pods exist in the target application, but only some of
   them use the `main` container, limit where the Hook can run with a
   `LabelSelector`. For example, this is done by adding labels to the
   appropriate Pods.
1. Hooks can have either `labelSelector` or `nameSelector` specified for
   selecting the resource of interest or both can be mentioned. In case both
   are defined, OR condition will be applied.
1. In the above example, `ALL_NAMESPACES` has been used in the recipe as
   variable. These are called as recipe parameters and needs to be defined in
   the DRPC under the section spec.kubeObjectProtection.recipeParameters. For
   example,

   ```yaml
   spec:
     kubeObjectProtection:
       recipeParameters:
         ALL_NAMESPACES:
         - e2e-disapp-recipe-hooks-deploy-rbd-busybox
   ```
