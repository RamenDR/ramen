# Ramen test applications

This directory contains test applications for testing disaster recovery
flows using the drenv environment.

The applications are kustomized for minikube based clusters. To test on
OpenShift clusters, use the
[RamenDR ocm-ramen-samples repository](https://github.com/RamenDR/ocm-ramen-samples).

## Channel

Channel pointing to ramen github repo. Must be installed to use these
applications.

## Bases

- `busybox`: Base busybox application. To create an actual application
  create an overlay and kustomize namespace and the pvc
  storageClassName.  See `*/busybox/kustomization.yaml` for example.

- `subscription`: Base busybox subscription. To create an actual
  subscription create an overlay and kustomize the namespace and
  github-path annotation. See `*/subscription/kustomization.yaml` for
  example.

- `dr`: Base drpc resource. To create an actual drpc, create an overlay
  and kustomize the namespace. See `*/dr/kustomization` for example.

## Overlays

- `rbd`: busybox, subscription, and dr overlays for testing replication
  with RBD mirroring.

- `hostpath`: busybox, subscription, and dr overlays for testing
  replication using `csi-hostpath-sc` storage class via `volsync`.

## Deployment

1. Install the channel

   ```
   kubectl apply -k channel --context hub
   ```

   This install a `ramen-gitops` channel in the `ramen-test` namespace,
   pointing the ramen repo on github.

1. Install the subscriptions

   ```
   kubectl apply -k rbd/subscription --context hub
   ```

   This installs the `busybox-sub` subscription in in the `busybox-rbd`
   namespace on the 'hub' cluster, and the `busybox` application in the
   `busybox-rbd` namespace in cluster selected by OCM.

   To install the `hostpath` subscription replace `rbd` with `hostpath`.
   The subscription and applications are installed in the
   `busybox-hostpath` namespace.

1. Make ramen the scheduler for the application

   ```
   kubectl patch placementrule busybox-placement \
       --namespace busybox-rbd \
       --patch '{"spec": {"schedulerName": "ramen"}}' \
       --type merge \
       --context hub
   ```

   This makes ramen the scheduler for this application, so OCM will not
   manage it.

1. Edit the drpc preferredCluster to dr1 or dr2, based on the cluster
   selected by OCM.

   ```
   $ grep -A1 preferredCluster test/apps/rbd/dr/kustomization.yaml
           path: /spec/preferredCluster
           value: dr2
   ```

1. Install the drpc

   ```
   kubectl apply -k rbd/dr --context hub
   ```

   This installs the drpc resource for the application. Ramen will
   take over and starting managing the application.

## Testing hostpath (volsync)

Ramen is assuming system managed security context, running applications
as unprivileged user. The sample applications run as root, so we need to
enable privileged movers in volsync. To test hostpath variant you need
to add this annotation to the application namespace on both clusters:

```
kubectl annotate ns/elevated-demo volsync.backube/privileged-movers=true
```
