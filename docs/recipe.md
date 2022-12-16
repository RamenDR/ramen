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
- name: volumes
  type: volume
  labelSelector: app=my-app
- name: config
  backupRef: config
  type: resource
  includedResourceTypes:
  - configmap
  - secret
- name: deployments
  backupRef: deployments
  type: resource
  includedResourceTypes:
  - deployment
- name: instance-resources
  backupRef: instance-resources
  type: resource
  excludedResourceTypes:
  - configmap
  - secret
  - deployment
hooks:
- name: service-hooks
  namespace: my-app-ns
  labelSelector: shouldRunHook=true
  ops:
  - name: pre-backup
    container: main
    command: ["/scripts/pre_backup.sh"]  # must exist in 'main' container
    timeout: 1800
  - name: post-restore
    container: main
    command: ["/scripts/post_restore.sh"]  # must exist in 'main' container
    timeout: 3600
workflows:
- name: capture  # referenced in VRG
  sequence:
  - group: config
  - group: deployments
  - hook: service-hooks/pre-backup
  - group: instance-resources
- name: recover  # referenced in VRG
  sequence:
  - group: config
  - group: deployments
  - group: instance-resources
  - hook: service-hooks/post-restore
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
    recipe:
      name: recipe-sample
      workflow:
        captureName: capture
        recoverName: recover
        volumeGroupName: volumes
```

### Additional information about sample Recipe and VRG

There are several parts of this example to be aware of:

1. Both the VRG and Recipe need to exist in the same Namespace.
1. Once the VRG has the Recipe information on it, users only need to update the
   Recipe itself to change the Capture/Protect order or types.
1. Capture and Recover actions are performed on the VRG, not by the Recipe. This
   includes the scheduling interval, s3 profile information, and sync/async configuration.
1. Groups can be referenced by arbitrary sequences. If they apply to both a Capture
  Workflow and a Recover Workflow, the group may be reused.
1. In order to run Hooks, the relevant Pods and containers must be running before
   the Hook is executed. This is the responsibility of the user and application,
   and Recipes do not check for valid Pods prior to running a Workflow.
1. Hooks may use arbitrary commands, but they must be able to run on a valid container
   found within the app. In the example above, a Pod with container `main` has
   scripts appropriate for running Hooks. Be aware that by default, Hooks will
   attempt to run on all valid Pods and search for the specified container. If
   several Pods exist in the target application, but only some of them use the
   `main` container, limit where the Hook can run with a `LabelSelector`. In the
   example above, this is done by adding `shouldRunHook=true` labels to the appropriate
   Pods.
