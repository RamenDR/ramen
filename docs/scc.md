# Security Context Constraints

Each namespace in an OpenShift cluster is assigned unique user identifier (UID)
and group identifier (GID) ranges and a Security-Enhanced Linux (SELinux) level
label.  For example:

   ```sh
   $ oc get ns mongodb-operator -ojsonpath='{.metadata.annotations}'|jq
   {
     "openshift.io/sa.scc.mcs": "s0:c29,c4",
     "openshift.io/sa.scc.supplemental-groups": "1000820000/10000",
     "openshift.io/sa.scc.uid-range": "1000820000/10000"
   }
   ```

By default, in OpenShift 4.11 and greater, the `restricted-v2` security context
constraint (SCC) requires each container run as a user and group within its
namespace's specified ranges.  The UID and GID default to their namespace's
minimum value.

## Problem

A user owns files it creates and others may not be permitted access.
For example:

   ```sh
   $ kubectl exec -it –nmongodb-operator po/example-openshift-mongodb-0 -cmongod -- ls -lZ /data
   -rw-------. 1 1000820000 1000820000 system_u:object_r:container_file_t:s0:c4,c29  118784 May 15 20:06 WiredTiger.wt
   ```

If a pod in another namespace were to attempt to access this file it would
likely be denied due to it running as a different user.  For example:

   ```
   2023-05-09T20:10:01.069+0000 E  STORAGE  [initandlisten] WiredTiger error (1) [1683663001:69308][1:0x7f6297d3eb00], file:WiredTiger.wt, connection: __posix_open_file, 667: /data/WiredTiger.wt: handle-open: open: Operation not permitted Raw: [1683663001:69308][1:0x7f6297d3eb00], file:WiredTiger.wt, connection: __posix_open_file, 667: /data/WiredTiger.wt: handle-open: open: Operation not permitted
   ```

Stateful application failover and relocation to another cluster are use cases
that expect to access a volume or volume replica in another namespace.  In this
example, the namespace on the other cluster is named the same but assigned
different values:

   ```sh
   $ oc get ns mongodb-operator -ojsonpath='{.metadata.annotations}'|jq
   {
     "openshift.io/sa.scc.mcs": "s0:c28,c17",
     "openshift.io/sa.scc.supplemental-groups": "1000790000/10000",
     "openshift.io/sa.scc.uid-range": "1000790000/10000"
   }
   ```

## Proposed solution

### Create new SCC for each namespace

Administrator defines a cluster-scoped SCC that specifies the source
namespace's UID range, GID range, and SELinux level label.

1. Start from an existing SCC such as the built-in `nonroot-v2` or `anyuid`

   ```sh
   oc get -oyaml scc/nonroot-v2 >/tmp/nonroot-v2.yaml
   cp /tmp/nonroot-v2.yaml /tmp/mongodb-operator.yaml
   ```

1. Modify it in several places

   ```sh
   vim /tmp/mongodb-operator.yaml
   diff -u /tmp/nonroot-v2.yaml /tmp/mongodb-operator.yaml
   ```

   1. Change its `name`

      ```diff
       metadata:
      -  name: nonroot-v2
      +  name: mongodb-operator
      ```

   1. Specify UID range

      ```diff
       runAsUser:
      -  type: MustRunAsNonRoot
      +  type: MustRunAs
      +  uid: 1000820000
      +  uidRangeMax: 1000829999
      +  uidRangeMin: 1000820000
      ```

   1. Specify GID range

      ```diff
       fsGroup:
      -  type: RunAsAny
      +  ranges:
      +  - max: 1000829999
      +    min: 1000820000
      +  type: MustRunAs
       supplementalGroups:
      -  type: RunAsAny
      +  ranges:
      +  - max: 1000829999
      +    min: 1000820000
      +  type: MustRunAs
      ```

   1. Specify SELinux level label

      ```diff
       seLinuxContext:
      +  seLinuxOptions:
      +    level: s0:c29,c4
      ```

   1. Remove `ownerReference` so that SCC doesn't get deleted

      ```diff
       metadata:
      -  ownerReferences:
      -  - apiVersion: config.openshift.io/v1
      -    kind: ClusterVersion
      -    name: version
      -    uid: 5fd923d9-7fb9-4473-b7f8-e7f372d4ea58
      ```

1. Create SCC on destination cluster

   ```sh
   $ oc create -f /tmp/mongodb-operator.yaml
   securitycontextconstraints.security.openshift.io/mongodb-operator created
   ```

1. Create `role` that `use`s SCC

   ```sh
   $ oc create role mongodb-operator --verb use --resource securitycontextconstraints --resource-name mongodb-operator -nmongodb-operator
   role.rbac.authorization.k8s.io/mongodb-operator created
   ```

1. Determine pod's service account name

   ```sh
   $ oc get pod -ocustom-columns=Namespace:.metadata.namespace,Name:.metadata.name,ServiceAccount:.spec.serviceAccountName -nmongodb-operator
   Namespace         Name               ServiceAccount
   mongodb-operator  example-mongodb-0  mongodb-database
   ```

1. Create `rolebinding` that associates SCC with pod's `serviceaccount`

   ```sh
   $ oc create rolebinding mongodb-operator --role mongodb-operator --serviceaccount mongodb-operator:mongodb-database -nmongodb-operator
   rolebinding.rbac.authorization.k8s.io/mongodb-operator created
   ```

This solution allows an application to access its files, but specifies
non-default security context constraints that may conflict with those of another
namespace.  Administrators accept this risk by creating such an SCC and may
automate it using a `Recipe` hook.

## Alternative solutions

### Use more permissive SCC and specify security context in pod

Application developer or user specifies UID, GID, and SELinux level label in
pod spec's `securityContext` instead of SCC and binds pod's `serviceaccount`
to a built-in `clusterrole` that `use`s a built-in SCC that permits the
specified UID and GID `nonroot-v2` or `anyuid`.

- *Advantage*: Administrator not required to create an SCC
- *Disadvantage*: `securityContext` must be specified for each pod

### Use default SCC and modify file permissions

Change each files' owner, group, and SELinux level label to match
destination namespace's.  This may be performed by
[Kubernetes](https://kubernetes.io/docs/tasks/configure-pod-container/security-context/#configure-volume-permission-and-ownership-change-policy-for-pods)
or [delegated to volume's CSI driver](https://kubernetes.io/docs/tasks/configure-pod-container/security-context/#delegating-volume-permission-and-ownership-change-to-csi-driver).

- *Disadvantage*: Delays pod start until all files' metadata are updated
- *Advantage*: Uses the default SCC which uses namespace's unique UID and
  GID ranges and SELinux level label
