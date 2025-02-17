// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package core

// spec.groups.includeResourceTypes is skipped which infers include all resource types for group workflow(backup/restore) operation
// spec.groups.essential is skipped which infers group workflow action should succeed else stop further processing of workflow and initiate rollback
const (
	VMRecipeName = "vm-recipe"
	VMRecipe     = `
apiVersion: ramendr.openshift.io/v1alpha1
kind: Recipe
metadata:
  name: vm-recipe
  namespace: ramen-ops
spec:
  appType: vm-app
  groups:
  - backupRef: vm-backup
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
    - ${ALL_NAMESPACES}
    labelSelector: 
      matchExpressions:
      - key: appname
        operator: In
        values:
        - ${LABEL_SELECTOR}
    name: vm-backup
    type: resource
  workflows:
  - failOn: any-error
    name: backup
    sequence:
    - group: vm-backup
  - failOn: any-error
    name: restore
    sequence:
    - group: vm-backup
  volumes:
    includedNamespaces:
    - ${ALL_NAMESPACES}
    name: vm-volumes
    type: volume
    labelSelector:
      matchExpressions:
      - key: appname
        operator: In
        values:
        - vm
`
)
