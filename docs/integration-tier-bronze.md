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
  ramendr.openshift.io/replicationID=replication-1 \
  --context cluster-1

kubectl label storageclass <storage-class-name> \
  ramendr.openshift.io/storageID=storage-cluster-2 \
  ramendr.openshift.io/replicationID=replication-1 \
  --context cluster-2
```

The `replicationID` must match across both clusters to indicate they
form a replication pair. The `storageID` values determine the DR
topology:

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
spec:
  provisioner: <csi-driver-provisioner-name>
  parameters:
    schedulingInterval: "1h"
```

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

**Ramen-side (hub cluster):**

1. Set the failover action on the DRPC (on the hub):

   ```bash
   kubectl edit drpc <drpc-name> -n <app-namespace> --context hub
   ```

   ```yaml
   spec:
     action: Failover
     failoverCluster: cluster-2
   ```

2. If cluster-1 is still accessible, scale down the application
   workload to stop writes and avoid split-brain:

   ```bash
   kubectl scale deployment <app-name> --replicas=0 \
     -n <app-namespace> --context cluster-1
   ```

   If cluster-1 is completely unavailable, skip this step — there are
   no active writes to worry about.

**Storage-side (manual, outside Ramen):**

3. If the cluster-1 storage is still accessible, demote the replicated
   volumes on the cluster-1 site to read-only to prevent stale writes.

4. On the storage backend's management interface, promote the
   replicated volumes on the cluster-2 site to read-write.

**Ramen-side (managed cluster):**

5. Ramen creates a VRG on cluster-2 with `spec.replicationState:
   secondary` and `spec.action: Failover`. The VRG restores PV metadata
   from S3, recreates the PVs, and creates VolumeReplication CRs for
   each protected PVC.

6. Ramen is now **blocked** waiting for the VR status conditions to
   indicate that the volumes are ready. Since the CSI driver does not
   update these, the administrator must patch each VR and VGR to
   unblock Ramen. Both resource types use the same status conditions —
   the only difference is the resource name in the command:

   ```bash
   # Patch a VolumeReplication
   kubectl patch volumereplication <vr-name> \
     --subresource=status --type=merge --context cluster-2 -p \
     '{"status":{"conditions":[
       {"type":"Completed","status":"True",
        "observedGeneration":1,
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"Promoted","message":"promoted to primary"},
       {"type":"Degraded","status":"False",
        "observedGeneration":1,
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"Healthy","message":"not degraded"},
       {"type":"Resyncing","status":"False",
        "observedGeneration":1,
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"NotResyncing","message":"not resyncing"}
     ]}}'

   # Patch a VolumeGroupReplication (same conditions)
   kubectl patch volumegroupreplication <vgr-name> \
     --subresource=status --type=merge --context cluster-2 -p \
     '{"status":{"conditions":[
       {"type":"Completed","status":"True",
        "observedGeneration":1,
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"Promoted","message":"promoted to primary"},
       {"type":"Degraded","status":"False",
        "observedGeneration":1,
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"Healthy","message":"not degraded"},
       {"type":"Resyncing","status":"False",
        "observedGeneration":1,
        "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "reason":"NotResyncing","message":"not resyncing"}
     ]}}'
   ```

   A future MCO UI enhancement will allow this via a button press.

7. Check which VRs or VGRs are still pending a status patch:

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

8. Once all VR conditions are satisfied, Ramen completes the failover:
   PVCs are created, the application is placed on cluster-2, and the
   DRPC status transitions to `FailedOver`.

## Failback Flow

After the primary site recovers, the user relocates the workload back
to cluster-1.

**Storage-side (manual, outside Ramen):**

1. Re-establish replication from cluster-2 (current primary) to
   cluster-1 on the storage backend.
2. Wait for the initial sync to complete so cluster-1 has a consistent
   copy of all data.

**Ramen-side:**

3. Set the relocate action on the DRPC (on the hub):

   ```bash
   kubectl edit drpc <drpc-name> -n <app-namespace> --context hub
   ```

   ```yaml
   spec:
     action: Relocate
     preferredCluster: cluster-1
   ```

4. Ramen initiates the relocate sequence. It sets
   `spec.prepareForFinalSync` and then `spec.runFinalSync` on the VRG
   to coordinate a final data sync before switchover.

5. Ramen is again **blocked** waiting for VR conditions on both
   clusters. The administrator must:

   - Ensure the final data sync is complete on the storage backend.

   - Patch the VR status on **cluster-2** to reflect successful
     demotion:

     ```bash
     kubectl patch volumereplication <vr-name> \
       --subresource=status --type=merge -p \
       '{"status":{"conditions":[
         {"type":"Completed","status":"True",
          "observedGeneration":1,
          "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
          "reason":"Demoted","message":"demoted to secondary"},
         {"type":"Degraded","status":"False",
          "observedGeneration":1,
          "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
          "reason":"Healthy","message":"not degraded"},
         {"type":"Resyncing","status":"False",
          "observedGeneration":1,
          "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
          "reason":"NotResyncing","message":"not resyncing"}
       ]}}' \
       --context cluster-2
     ```

   - Patch the VR status on **cluster-1** to reflect successful
     promotion:

     ```bash
     kubectl patch volumereplication <vr-name> \
       --subresource=status --type=merge -p \
       '{"status":{"conditions":[
         {"type":"Completed","status":"True",
          "observedGeneration":1,
          "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
          "reason":"Promoted","message":"promoted to primary"},
         {"type":"Degraded","status":"False",
          "observedGeneration":1,
          "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
          "reason":"Healthy","message":"not degraded"},
         {"type":"Resyncing","status":"False",
          "observedGeneration":1,
          "lastTransitionTime":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
          "reason":"NotResyncing","message":"not resyncing"}
       ]}}' \
       --context cluster-1
     ```

   Use the same commands from step 7 of the failover flow to check
   which VRs/VGRs are still pending on each cluster.

6. Once all VR conditions are satisfied, Ramen completes the relocate:
   the application is placed on cluster-1 and the DRPC status
   transitions to `Relocated`.
