// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	// "context"
	// "encoding/json"
	// "fmt"
	// "os"
	// "strings"

	// "github.com/go-logr/logr"
	// ramen "github.com/ramendr/ramen/api/v1alpha1"
	// "github.com/ramendr/ramen/internal/controller/kubeobjects"
	// "github.com/ramendr/ramen/internal/controller/util"
	// recipe "github.com/ramendr/recipe/api/v1alpha1"
	// "golang.org/x/exp/slices"
	// "k8s.io/apimachinery/pkg/types"
	// "k8s.io/apimachinery/pkg/util/sets"
	// "sigs.k8s.io/controller-runtime/pkg/builder"
	// "sigs.k8s.io/controller-runtime/pkg/client"
	// "sigs.k8s.io/controller-runtime/pkg/handler"
	// "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
  VMRecipeName                    = "vm-recipe"
  VMRecipe=`
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

