// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package api

const (
  VMRecipeName = "vm-recipe"
  VMRecipe = `
apiVersion: ramendr.openshift.io/v1alpha1
kind: Recipe
metadata:
  name: vm-recipe
  namespace: ramen-ops
spec:
  appType: vm-dv
  groups:
  - backupRef: vm-dv
    excludedResourceTypes:
    - events
    - event.events.k8s.io
    - persistentvolumes
    - replicaset
    - persistentvolumeclaims
    - pods
    includedResources:
    - namespaces
    - nodes
    includedNamespaces:
    - vm-dv
    labelSelector:
      matchExpressions:
      - key: appname
        operator: In
        values:
        - vm
    name: vm-dv
    type: resource
  workflows:
  - failOn: any-error
    name: backup
    sequence:
    - group: vm-dv
  - failOn: any-error
    name: restore
    sequence:
    - group: vm-dv
  volumes:
    includedNamespaces:
    - vm-dv
    name: varlog
    type: volume
    labelSelector:
      matchExpressions:
      - key: appname
        operator: In
        values:
        - vm
`
)

