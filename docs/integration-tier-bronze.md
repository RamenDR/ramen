<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Bronze Tier — Detailed Setup and Operations

This document covers the prerequisites and operational flows for
[Bronze tier](integration-tiers.md#bronze--no-vendor-changes-required)
storage integrations, where the storage vendor's CSI driver does not
implement the csi-addons replication CRDs.

## Architecture Overview

Ramen's disaster recovery architecture involves three OpenShift
clusters:

- **Hub cluster** — Runs Red Hat Advanced Cluster Management (ACM) and
  the Multicluster Orchestrator (MCO). The hub does not run application
  workloads itself. Instead, it acts as the central control plane: it
  manages the two managed clusters, defines DR policies, and
  orchestrates failover and failback operations by directing which
  managed cluster should run a given application.

- **cluster-1** (managed cluster) — One of two clusters where
  applications run. In normal operation this is the **primary** site:
  it runs the application workloads and its storage backend replicates
  data to cluster-2.

- **cluster-2** (managed cluster) — The **secondary** site. It
  receives replicated data from cluster-1 and stands ready to take over
  application workloads if cluster-1 fails or during a planned
  migration.

The hub cluster runs the Ramen Hub Operator, which watches
`DRPlacementControl` (DRPC) resources to coordinate DR operations. Each
managed cluster runs the Ramen DR Cluster Operator, which manages
`VolumeReplicationGroup` (VRG) resources to handle PVC replication and
PV metadata protection locally.

ACM includes Open Cluster Management (OCM), an open-source framework
for managing multiple Kubernetes clusters from a single hub. OCM runs
on the hub cluster and provides the Placement API, which controls
**which managed cluster** a given application is deployed to. Ramen
extends this mechanism: during a DR event, it updates the OCM placement
decision on the hub to move the application from one managed cluster to
the other, while coordinating the underlying storage operations.

### Application Types

Ramen protects persistent data (PVCs) for all application types, but
the way **application resources** (Deployments, ConfigMaps, etc.) are
handled depends on how the application is deployed:

- **GitOps / ArgoCD applications** — The application definition lives
  in a Git repository and is deployed via ArgoCD ApplicationSets that
  reference an OCM Placement. During DR, Ramen updates the placement
  decision and ArgoCD automatically deploys the application on the new
  target cluster from the same Git source. No separate backup of
  application resources is needed.

- **Discovered applications** — Applications deployed through other
  means (e.g., `kubectl apply`, Helm, kustomize) that are not managed
  by GitOps. Since there is no Git source to redeploy from, Ramen uses
  [Velero](https://velero.io/) to automatically back up the
  application's Kubernetes resources to S3 and restore them on the
  target cluster during DR.

- **Recipe-based applications** — An extension of discovered
  applications for complex stateful workloads that need more than a
  simple backup and restore. [Recipes](recipe.md) define
  vendor-supplied or user-written workflows that specify capture and
  recovery steps with proper sequencing and custom hooks (e.g.,
  quiescing a database before snapshot, running post-restore
  validation).

## Initial Setup

This section walks through the one-time setup required before any DR
operations can be performed. All steps are performed on the **hub
cluster** unless noted otherwise.

### 1. Configure S3 Storage for Metadata

Ramen stores PV metadata in S3 so it can reconstruct volumes on the
peer cluster during failover. Both clusters must be able to access the
same S3 bucket. Create an S3 secret on each managed cluster:

```bash
kubectl create secret generic s3-secret \
  --from-literal=AWS_ACCESS_KEY_ID=<key> \
  --from-literal=AWS_SECRET_ACCESS_KEY=<secret> \
  -n ramen-system --context cluster-1

kubectl create secret generic s3-secret \
  --from-literal=AWS_ACCESS_KEY_ID=<key> \
  --from-literal=AWS_SECRET_ACCESS_KEY=<secret> \
  -n ramen-system --context cluster-2
```

Add an S3 profile to the `ramen-hub-operator-config` ConfigMap in the
`ramen-system` namespace on the hub. Both clusters share the same
profile so that either can read PV metadata uploaded by its peer:

```yaml
s3StoreProfiles:
  - s3ProfileName: s3-profile
    s3Bucket: ramen-bucket
    s3Region: us-east-1
    s3CompatibleEndpoint: https://s3.example.com
    s3SecretRef:
      name: s3-secret
      namespace: ramen-system
```

### 2. Configure Storage Replication (Manual)

On the storage backend's management interface, configure replication
between the two sites. This is entirely vendor-specific — Ramen is not
involved in this step. Ensure that volumes provisioned by the CSI driver
on cluster-1 are replicated to cluster-2 and vice versa.

### 3. Label StorageClasses on Managed Clusters

On **each managed cluster**, label the StorageClass used by protected
applications so Ramen can discover the storage relationship:

```bash
kubectl label storageclass <storage-class-name> \
  ramendr.openshift.io/storageID=storage-cluster-1 \
  ramendr.openshift.io/groupreplicationid=replication-1 \
  ramendr.openshift.io/offloaded=true \
  --context cluster-1

kubectl label storageclass <storage-class-name> \
  ramendr.openshift.io/storageID=storage-cluster-2 \
  ramendr.openshift.io/groupreplicationid=replication-1 \
  ramendr.openshift.io/offloaded=true \
  --context cluster-2
```

The `groupreplicationid` must match across both clusters to indicate
they form a replication pair. The `offloaded` label tells Ramen that
replication is managed externally by the storage backend, not by
Ramen itself. The `storageID` values determine the DR topology:

- **Regional DR (async):** Use **different** `storageID` values on each
  cluster — this tells Ramen the storage backends are independent and
  replication is asynchronous.
- **Metro DR (sync):** Use the **same** `storageID` on both clusters —
  this tells Ramen the clusters share a single synchronously replicated
  storage backend.

The example above uses Regional DR (different `storageID` per cluster),
which is the typical Bronze tier configuration.

### 4. Create Stub Replication Classes on Managed Clusters

Because the CSI driver does not implement the csi-addons replication
CRDs, stub `VolumeReplicationClass` and `VolumeGroupReplicationClass`
resources must be created on **each managed cluster** so that Ramen can
proceed with VR/VGR creation. The CSI driver does not need to act on
these — they exist solely to unblock Ramen's reconciliation logic.

```yaml
apiVersion: replication.storage.openshift.io/v1alpha1
kind: VolumeReplicationClass
metadata:
  name: <storage-vendor>-vrc
  labels:
    ramendr.openshift.io/replicationid: replication-1
spec:
  provisioner: <csi-driver-provisioner-name>
  parameters:
    schedulingInterval: "1h"
---
apiVersion: replication.storage.openshift.io/v1alpha1
kind: VolumeGroupReplicationClass
metadata:
  name: <storage-vendor>-vgrc
  labels:
    ramendr.openshift.io/groupreplicationid: replication-1
    ramendr.openshift.io/storageid: storage-cluster-1
    ramendr.openshift.io/global: "true"
spec:
  provisioner: <csi-driver-provisioner-name>
  parameters:
    schedulingInterval: "1h"
```

The `global: "true"` label indicates that VGR resources created from
this class are globally scoped — a single Global VGR per `storageid`
manages all PVCs on that storage backend, rather than one VGR per
application. This is the model used by IBM Fusion Access and other
storage backends where replication occurs at the LUN or filesystem
level.

The `storageid` on the VGRClass must match the `storageID` label on the
corresponding StorageClass. Note that the VGRClass `storageid` value is
**cluster-local** — each cluster has its own value, unlike
`groupreplicationid` which must match across clusters.

The `schedulingInterval` value is not used operationally in Bronze
tier — Ramen never passes it to the VolumeReplication CR and does not
use it to schedule replication. It only needs to **match** the
DRPolicy's `schedulingInterval` exactly so that Ramen's class selection
logic can find a compatible VRC.

### 5. Create DRCluster Resources

Create a DRCluster for each managed cluster on the hub:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRCluster
metadata:
  name: cluster-1
spec:
  s3ProfileName: s3-profile
---
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRCluster
metadata:
  name: cluster-2
spec:
  s3ProfileName: s3-profile
```

Verify both DRClusters reach the `Validated` condition:

```bash
kubectl get drcluster --context hub
```

### 6. Create a DRPolicy

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPolicy
metadata:
  name: bronze-dr-policy
spec:
  drClusters:
    - cluster-1
    - cluster-2
  schedulingInterval: "1h"
```

The `schedulingInterval` must match the stub VRC/VGRC parameter. Verify
the DRPolicy discovers the peer class relationship:

```bash
kubectl get drpolicy bronze-dr-policy -o yaml --context hub
```

Check that `status.async.peerClasses` is populated.

### 7. Protect an Application with DRPC

The application can be any workload with persistent storage — a
Deployment, StatefulSet, KubeVirt VM, or any other resource that uses
PVCs. It can be deployed by any means (GitOps/ArgoCD, Helm, `kubectl
apply`, etc.). The only requirement is that an OCM Placement resource
exists for the application, so that Ramen can control which managed
cluster it runs on.

Create a DRPC in the same namespace as the application on the hub:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: app-drpc
  namespace: app-namespace
spec:
  preferredCluster: cluster-1
  drPolicyRef:
    name: bronze-dr-policy
  placementRef:
    kind: Placement
    name: app-placement
  pvcSelector:
    matchLabels:
      app: myapp
```

`pvcSelector` selects only PVCs for replication — it does not affect
which other Kubernetes resources are protected.

For discovered and recipe-based applications, Ramen uses Velero to
capture the application's Kubernetes resources (Deployments, Services,
ConfigMaps, etc.) to S3, so they can be restored on the target cluster
during failover. By default, Velero captures all resources in the
application's namespace. This can be narrowed with an optional
`kubeObjectProtection.kubeObjectSelector` label selector, or fully
customized with a Recipe. Ramen also uploads PV metadata to S3
separately.

GitOps-deployed applications do not need Velero backup — they are
redeployed from the Git source by ArgoCD on the target cluster. Ramen
still uploads PV metadata to S3 for these applications.

## Failover Flow

This example assumes cluster-1 (primary) has suffered a failure and the
user is failing over to cluster-2.

1. **Ramen — hub cluster:** Set the failover action on the DRPC:

   ```bash
   kubectl edit drpc <drpc-name> -n <app-namespace> --context hub
   ```

   ```yaml
   spec:
     action: Failover
     failoverCluster: cluster-2
   ```

1. **Ramen — hub cluster:** Ramen creates a VRG on cluster-2 with
   `spec.replicationState: primary` and `spec.action: Failover`. The
   VRG restores PV metadata from S3 and recreates the PVs.

1. **Ramen — hub cluster:** For managed apps, ACM automatically
   handles scaling down the application on cluster-1. For discovered
   apps, if cluster-1 is still accessible, manually scale down the
   workload to stop writes:

   ```bash
   kubectl scale deployment <app-name> --replicas=0 \
     -n <app-namespace> --context cluster-1
   ```

   If cluster-1 is completely unavailable, skip this step.

1. **Storage-side (manual):** If the cluster-1 storage is still
   accessible, demote the replicated volumes on the cluster-1 site to
   read-only to prevent stale writes.

1. **Storage-side (manual):** On the storage backend's management
   interface, promote the replicated volumes on the cluster-2 site to
   read-write.

   **Fencing warning:** Bronze tier does not have NetworkFence support.
   If cluster-1 is unreachable and cannot be demoted above, there
   is no automated mechanism to prevent it from continuing to write to
   shared storage. This creates a risk of data divergence (split-brain)
   until storage-level fencing is resolved manually on the storage
   backend. Silver and Gold tiers avoid this risk through automated
   NetworkFence CRDs.

1. **Ramen — managed cluster:** Ramen is now **blocked** waiting for
   VGR/VR status to confirm that the volumes are ready on cluster-2.
   Since the CSI driver does not update the status, the administrator
   must patch each VGR and VR to unblock Ramen.

   First, fetch the current resource generation (Ramen validates
   `observedGeneration` against `metadata.generation` and rejects stale
   conditions):

   ```bash
   GEN=$(kubectl get volumegroupreplication <vgr-name> \
     --context cluster-2 -o jsonpath='{.metadata.generation}')
   ```

   Then patch the VGR status with `.state: Primary` and the required
   conditions:

   ```bash
   kubectl patch volumegroupreplication <vgr-name> \
     --subresource=status --type=merge --context cluster-2 -p \
     '{"status":{"state":"Primary","conditions":[
       {"type":"Completed","status":"True",
        "observedGeneration":'$GEN',
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"Promoted","message":"promoted to primary"},
       {"type":"Degraded","status":"False",
        "observedGeneration":'$GEN',
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"Healthy","message":"not degraded"},
       {"type":"Resyncing","status":"False",
        "observedGeneration":'$GEN',
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"NotResyncing","message":"not resyncing"}
     ]}}'
   ```

   Apply the same pattern for each VolumeReplication resource (same
   conditions and `.status.state: Primary`, using the VR's own
   `metadata.generation`):

   ```bash
   GEN=$(kubectl get volumereplication <vr-name> \
     --context cluster-2 -o jsonpath='{.metadata.generation}')

   kubectl patch volumereplication <vr-name> \
     --subresource=status --type=merge --context cluster-2 -p \
     '{"status":{"state":"Primary","conditions":[
       {"type":"Completed","status":"True",
        "observedGeneration":'$GEN',
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"Promoted","message":"promoted to primary"},
       {"type":"Degraded","status":"False",
        "observedGeneration":'$GEN',
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"Healthy","message":"not degraded"},
       {"type":"Resyncing","status":"False",
        "observedGeneration":'$GEN',
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"NotResyncing","message":"not resyncing"}
     ]}}'
   ```

   A future MCO UI enhancement will allow this via a button press.

1. Check which VRs or VGRs are still pending a status patch:

   ```bash
   # VRs without a Completed=True condition
   kubectl get volumereplication -A -o json --context cluster-2 | \
     jq -r '.items[] | select(
       (.status.conditions // [] | map(select(.type=="Completed" and .status=="True")) | length) == 0
     ) | "\(.metadata.namespace)/\(.metadata.name)"'

   # VGRs without a Completed=True condition
   kubectl get volumegroupreplication -A -o json --context cluster-2 | \
     jq -r '.items[] | select(
       (.status.conditions // [] | map(select(.type=="Completed" and .status=="True")) | length) == 0
     ) | "\(.metadata.namespace)/\(.metadata.name)"'
   ```

1. Once all VGR/VR status fields are satisfied, Ramen completes the
   failover: PVCs are created, the application is placed on cluster-2,
   and the DRPC status transitions to `FailedOver`.

## Failback (Relocate) Flow

After the primary site recovers, the user relocates the workload back
to cluster-1. Unlike failover, relocation is a planned operation — both
clusters are available and Ramen coordinates the transition through VGR
replicationState changes.

1. **Ramen — hub cluster:** Set the relocate action on the DRPC:

   ```bash
   kubectl edit drpc <drpc-name> -n <app-namespace> --context hub
   ```

   ```yaml
   spec:
     action: Relocate
     preferredCluster: cluster-1
   ```

1. **Ramen — hub cluster:** Ramen changes the VGR
   `spec.replicationState` from `Primary` to `Secondary` on cluster-2.
   This signals that cluster-2 should relinquish its primary role.

1. **Storage-side (manual):** On the storage backend, perform the final
   data sync from cluster-2 to cluster-1 and demote the cluster-2
   volumes. The VGR replicationState change to Secondary is the trigger
   to begin this process.

1. **Ramen — cluster-2 demotion acknowledgement:** Ramen is **blocked**
   waiting for VGR/VR status on cluster-2 to confirm the demotion.
   Patch each VGR and VR to acknowledge that the storage-side demotion
   and final sync are complete:

   ```bash
   GEN=$(kubectl get volumegroupreplication <vgr-name> \
     --context cluster-2 -o jsonpath='{.metadata.generation}')

   kubectl patch volumegroupreplication <vgr-name> \
     --subresource=status --type=merge --context cluster-2 -p \
     '{"status":{"state":"Secondary","conditions":[
       {"type":"Completed","status":"True",
        "observedGeneration":'$GEN',
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"Demoted","message":"demoted to secondary"},
       {"type":"Degraded","status":"False",
        "observedGeneration":'$GEN',
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"Healthy","message":"not degraded"},
       {"type":"Resyncing","status":"False",
        "observedGeneration":'$GEN',
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"NotResyncing","message":"not resyncing"}
     ]}}'
   ```

   Apply the same pattern for each VolumeReplication resource on
   cluster-2 (`.status.state: Secondary`, using the VR's own
   `metadata.generation`).

1. **Ramen — hub cluster:** Ramen sees the acknowledgement and proceeds
   to promote on cluster-1. It updates the VGR
   `spec.replicationState` to `Primary` on cluster-1.

1. **Storage-side (manual):** On the storage backend, promote the
   cluster-1 volumes to read-write.

1. **Ramen — cluster-1 promotion acknowledgement:** Patch each VGR and
   VR on cluster-1 to confirm the promotion:

   ```bash
   GEN=$(kubectl get volumegroupreplication <vgr-name> \
     --context cluster-1 -o jsonpath='{.metadata.generation}')

   kubectl patch volumegroupreplication <vgr-name> \
     --subresource=status --type=merge --context cluster-1 -p \
     '{"status":{"state":"Primary","conditions":[
       {"type":"Completed","status":"True",
        "observedGeneration":'$GEN',
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"Promoted","message":"promoted to primary"},
       {"type":"Degraded","status":"False",
        "observedGeneration":'$GEN',
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"Healthy","message":"not degraded"},
       {"type":"Resyncing","status":"False",
        "observedGeneration":'$GEN',
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"NotResyncing","message":"not resyncing"}
     ]}}'
   ```

   Apply the same pattern for each VolumeReplication resource on
   cluster-1. Use the pending-check commands from the failover flow to
   verify which VRs/VGRs are still pending on each cluster.

1. Once all VGR/VR status fields are satisfied on both clusters, Ramen
   completes the relocate: the application is placed on cluster-1 and
   the DRPC status transitions to `Relocated`.
