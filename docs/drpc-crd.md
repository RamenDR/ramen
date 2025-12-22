<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# DRPlacementControl CRD

## Overview

The **DRPlacementControl** (DRPC) custom resource is the primary interface
for protecting and managing applications with disaster recovery capabilities.
Created by users in the application namespace on the OCM hub cluster, it
orchestrates:

- Initial deployment of applications to the preferred cluster
- Failover operations to peer clusters during disasters
- Planned relocation between clusters

The DRPC is the control plane for application DR - it creates and manages
VolumeReplicationGroup (VRG) resources on managed clusters via ManifestWork,
coordinates with OCM Placement, and tracks DR state.

**Lifecycle:** One DRPC per application. Created when enabling DR protection,
modified to trigger DR operations, deleted when removing DR protection.

## API Group and Version

- **API Group:** `ramendr.openshift.io`
- **API Version:** `v1alpha1`
- **Kind:** `DRPlacementControl`
- **Short Name:** `drpc`
- **Scope:** Namespaced

## Spec Fields

### Required Fields

#### `placementRef` (v1.ObjectReference)

Reference to the OCM Placement or PlacementRule resource that selects where
the application runs.

**Requirements:**

- Must exist in the same namespace as DRPC
- Immutable after creation
- Supported kinds: `Placement`, `PlacementRule`

**Example:**

```yaml
placementRef:
  kind: Placement
  name: my-app-placement
```

**How it works:** DRPC modifies the Placement decisions to control which
cluster runs the application during DR operations.

#### `drPolicyRef` (v1.ObjectReference)

Reference to the DRPolicy that defines the DR topology and replication
configuration.

**Requirements:**

- DRPolicy must exist (cluster-scoped resource)
- Immutable after creation
- DRPolicy must have exactly 2 clusters

**Example:**

```yaml
drPolicyRef:
  name: dr-policy
```

#### `pvcSelector` (metav1.LabelSelector)

Label selector to identify which PVCs in the application namespace need DR
protection.

**Requirements:**

- Immutable after creation
- Must match labels on PVCs you want to protect

**Example:**

```yaml
pvcSelector:
  matchLabels:
    app: my-app
    protect: "true"
```

**Best practice:** Use specific labels to avoid protecting unwanted PVCs
(e.g., cache volumes).

### Optional Fields

#### `preferredCluster` (string)

The cluster name where the application should initially run and return to
after relocate.

**Behavior:**

- If set, application deploys to this cluster
- During relocate, application returns to this cluster
- Can be changed to relocate application

**Example:**

```yaml
preferredCluster: east-cluster
```

#### `failoverCluster` (string)

The target cluster for failover operations.

**When to use:** Set this along with `action: Failover` to trigger a failover.

**Example:**

```yaml
failoverCluster: west-cluster
action: Failover
```

**Note:** For relocate operations, change `preferredCluster` instead.

#### `action` (DRAction)

The DR action to perform: `Failover` or `Relocate`.

**Valid values:**

- `Failover` - Recover application on `failoverCluster` (assumes source is
  down)
- `Relocate` - Migrate application to target cluster (planned operation)

**Example:**

```yaml
# Trigger failover
action: Failover
failoverCluster: west-cluster
```

```yaml
# Trigger relocate
action: Relocate
preferredCluster: east-cluster
```

**State machine:** Set action to trigger operation, DRPC clears it when
complete.

#### `protectedNamespaces` ([]string)

List of additional namespaces to protect beyond the DRPC namespace.

**Requirements:**

- DRPC and PlacementRef must be in RamenOpsNamespace (from RamenConfig)
- Resources in these namespaces are treated as unmanaged
- Typically used with Recipes to control capture order

**Example:**

```yaml
protectedNamespaces:
  - app-namespace-1
  - app-namespace-2
  - app-config-namespace
```

**Use case:** Multi-namespace applications or shared configuration namespaces.

#### `kubeObjectProtection` (KubeObjectProtectionSpec)

Configuration for protecting Kubernetes resources (not just PVCs).

**Fields:**

- `captureInterval` (metav1.Duration) - How often to capture Kubernetes
  objects (default: 5m)
- `recipeRef` (RecipeRef) - Reference to Recipe for custom workflows
- `recipeParameters` (map[string][]string) - Parameters for Recipe
- `kubeObjectSelector` (metav1.LabelSelector) - Selector for objects to
  protect

**Example:**

```yaml
kubeObjectProtection:
  captureInterval: 5m
  recipeRef:
    name: mysql-recipe
    namespace: my-app
```

**When to use:**

- Discovered applications (no GitOps)
- Recipe-based protection for stateful apps
- Not needed for GitOps (Git is source of truth)

#### `volSyncSpec` (VolSyncSpec)

Configuration for VolSync-based replication (sync DR).

**Example:**

```yaml
volSyncSpec:
  disabled: false
```

## Status Fields

The DRPC status provides detailed information about the DR state and progress.

### `phase` (DRState)

Current state of the DRPC.

**States:**

- `WaitForUser` - Waiting for user action after hub recovery
- `Initiating` - Action (Deploy/Failover/Relocate) preparing for execution
- `Deploying` - Initial deployment in progress
- `Deployed` - Application running normally with DR protection
- `FailingOver` - Failover operation in progress
- `FailedOver` - Application has been failed over
- `Relocating` - Relocation operation in progress
- `Relocated` - Application has been relocated
- `Deleting` - DRPC deletion in progress

### `observedGeneration` (int64)

The generation of the DRPC spec that was last processed.

**Use:** Compare with `metadata.generation` to see if changes are processed.

### `actionStartTime` (metav1.Time)

When the current DR action started.

### `actionDuration` (metav1.Duration)

How long the current action has been running.

### `progression` (ProgressionStatus)

Detailed progress indicator for the current operation.

**Deploy progressions** (during initial deployment):

- `CreatingMW` - Creating ManifestWork for VRG
- `UpdatingPlRule` - Updating Placement Rule
- `EnsuringVolSyncSetup` - Setting up VolSync (if enabled)
- `SettingUpVolSyncDest` - Setting up VolSync destination
- `Completed` - Operation finished successfully

**Failover progressions** (during failover operation):

Pre-failover (before creating VRG on failover cluster):

- `CheckingFailoverPrerequisites` - Verifying failover prerequisites
- `WaitForFencing` - Waiting for storage fencing to complete
- `WaitForStorageMaintenanceActivation` - Waiting for storage maintenance mode

Post-failover (after creating VRG on failover cluster):

- `FailingOverToCluster` - Executing failover to target cluster
- `WaitingForResourceRestore` - Waiting for resources to restore
- `WaitForReadiness` - Waiting for application to become ready
- `UpdatedPlacement` - Placement has been updated
- `Completed` - Operation finished successfully
- `CleaningUp` - Cleaning up resources on source cluster
- `WaitOnUserToCleanUp` - Waiting for user intervention

**Relocate progressions** (during relocate operation):

Pre-relocate (before creating VRG on preferred cluster):

- `PreparingFinalSync` - Preparing for final data sync
- `ClearingPlacement` - Clearing placement decisions
- `RunningFinalSync` - Running final data sync
- `FinalSyncComplete` - Final sync completed
- `EnsuringVolumesAreSecondary` - Setting volumes to secondary state
- `WaitOnUserToCleanUp` - Waiting for user intervention (if needed)

Post-relocate (after creating VRG on preferred cluster):

- `WaitingForResourceRestore` - Waiting for resources to restore
- `WaitForReadiness` - Waiting for application to become ready
- `UpdatedPlacement` - Placement has been updated
- `Completed` - Operation finished successfully
- `CleaningUp` - Cleaning up resources on source cluster

**Special progressions** (any operation):

- `Deleting` - DRPC deletion in progress
- `Deleted` - DRPC has been deleted
- `Paused` - Action is paused, user intervention required

### `preferredDecision` (PlacementDecision)

The cluster where the application is currently running or should run.

**Fields:**

- `clusterName` - Name of the cluster
- `clusterNamespace` - Namespace of the ManagedCluster

### `conditions` ([]metav1.Condition)

Standard Kubernetes conditions.

**Condition types:**

- `Available` - Cluster is ready for workload
- `PeerReady` - Peer cluster is ready for DR operations
- `Protected` - Application is properly protected

### `lastGroupSyncTime` (metav1.Time)

Time of the most recent successful synchronization of all PVCs.

### `lastGroupSyncDuration` (metav1.Duration)

Duration of the most recent sync operation.

### `lastGroupSyncBytes` (int64)

Total bytes transferred in the most recent sync.

### `lastKubeObjectProtectionTime` (metav1.Time)

Time of the most recent successful Kubernetes object protection.

## Examples

### Example 1: Basic Application Protection

Enable DR for a simple stateless application with persistent data:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: webapp-drpc
  namespace: webapp
spec:
  # Deploy to east cluster initially
  preferredCluster: east-cluster

  # Use regional DR policy
  drPolicyRef:
    name: dr-policy

  # Reference OCM Placement
  placementRef:
    kind: Placement
    name: webapp-placement

  # Protect PVCs with app=webapp label
  pvcSelector:
    matchLabels:
      app: webapp
```

### Example 2: Triggering a Failover

Fail over application to west cluster due to east cluster failure:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: webapp-drpc
  namespace: webapp
spec:
  preferredCluster: east-cluster
  failoverCluster: west-cluster # Target cluster
  drPolicyRef:
    name: dr-policy
  placementRef:
    kind: Placement
    name: webapp-placement
  pvcSelector:
    matchLabels:
      app: webapp
  action: Failover # Trigger failover
```

### Example 3: Triggering a Relocate

Move application back to east cluster:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: webapp-drpc
  namespace: webapp
spec:
  preferredCluster: east-cluster # Target cluster
  drPolicyRef:
    name: dr-policy
  placementRef:
    kind: Placement
    name: webapp-placement
  pvcSelector:
    matchLabels:
      app: webapp
  action: Relocate # Trigger relocate
```

### Example 4: Recipe-Based Protection

Protect a database application with custom capture/recovery workflows:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: mysql-drpc
  namespace: mysql-app
spec:
  preferredCluster: east-cluster
  drPolicyRef:
    name: dr-policy
  placementRef:
    kind: Placement
    name: mysql-placement
  pvcSelector:
    matchLabels:
      app: mysql
  kubeObjectProtection:
    captureInterval: 5m
    recipeRef:
      name: mysql-recipe
      namespace: mysql-app
```

### Example 5: Multi-Namespace Protection

Protect application spanning multiple namespaces:

```yaml
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: multi-ns-app-drpc
  namespace: ramen-ops # Must be in RamenOpsNamespace
spec:
  preferredCluster: east-cluster
  drPolicyRef:
    name: dr-policy
  placementRef:
    kind: Placement
    name: multi-ns-app-placement
  pvcSelector:
    matchLabels:
      protect: "true"
  protectedNamespaces:
    - app-frontend
    - app-backend
    - app-database
  kubeObjectProtection:
    captureInterval: 5m
```

## Usage

### Enabling DR for an Application

**Prerequisites:**

1. Application deployed with OCM Placement
1. DRPolicy created
1. PVCs labeled appropriately

**Steps:**

1. Label your PVCs:

   ```bash
   kubectl label pvc my-pvc app=myapp -n myapp
   ```

1. Create the DRPC:

   ```bash
   kubectl apply -f drpc.yaml
   ```

1. Monitor the DRPC status:

   ```bash
   kubectl get drpc -n myapp
   kubectl describe drpc myapp-drpc -n myapp
   ```

1. Wait for `Deployed` phase:

   ```bash
   kubectl get drpc myapp-drpc -n myapp -o jsonpath='{.status.phase}'
   ```

### Performing a Failover

**Scenario:** East cluster has failed, need to recover on west cluster.

```bash
kubectl patch drpc myapp-drpc -n myapp --type merge -p '
{
  "spec": {
    "action": "Failover",
    "failoverCluster": "west-cluster"
  }
}'
```

**Monitor progress:**

```bash
kubectl get drpc myapp-drpc -n myapp -w
```

**Check status:**

```bash
kubectl get drpc myapp-drpc -n myapp -o yaml
```

Look for `phase: FailedOver` and `progression: Completed`.

### Performing a Relocate

**Scenario:** East cluster is healthy again, relocate application back.

```bash
kubectl patch drpc myapp-drpc -n myapp --type merge -p '
{
  "spec": {
    "action": "Relocate",
    "preferredCluster": "east-cluster"
  }
}'
```

**Monitor progress:**

```bash
kubectl get drpc myapp-drpc -n myapp -w
```

### Disabling DR Protection

To remove DR protection:

```bash
kubectl delete drpc myapp-drpc -n myapp
```

**Note:** This does NOT delete the application, only removes DR protection.

## Monitoring and Observability

### Check DRPC Phase

```bash
kubectl get drpc -A
```

Output shows: name, age, preferredCluster, failoverCluster, desiredState,
currentState.

### View Detailed Status

```bash
kubectl describe drpc myapp-drpc -n myapp
```

### Check Replication Status

```bash
kubectl get drpc myapp-drpc -n myapp -o jsonpath='{.status.lastGroupSyncTime}'
kubectl get drpc myapp-drpc -n myapp -o jsonpath='{.status.lastGroupSyncBytes}'
```

### View Conditions

```bash
kubectl get drpc myapp-drpc -n myapp -o jsonpath='{.status.conditions}' | jq
```

## Troubleshooting

### General Diagnosis

Check DRPC status and progression to identify issues:

```bash
kubectl get drpc myapp-drpc -n myapp
kubectl describe drpc myapp-drpc -n myapp
kubectl get drpc myapp-drpc -n myapp -o jsonpath='{.status.progression}'
```

### DRPC Stuck in Deploying

**Check:** Verify PVCs match the selector and VRG is created.

```bash
kubectl get pvc -n myapp -l app=myapp
kubectl get manifestwork -A | grep myapp
```

**Common causes:** PVC selector mismatch, storage class not found on target
cluster, or VRG not becoming ready.

**Solution:** Ensure PVCs have correct labels, storage class exists on managed
cluster, and check VRG status for errors.

### Failover Not Progressing

**Check:** Look for `WaitForFencing` or `WaitForReadiness` progression.

```bash
kubectl get drpc myapp-drpc -n myapp -o jsonpath='{.status.progression}'
kubectl get pods -n myapp --context west-cluster
```

**Common causes:** Storage fencing in progress, application pods not ready,
or S3 storage not accessible.

**Solution:** Wait for fencing to complete (normal for Metro DR), fix
application issues (image pull, resources), or verify S3 connectivity.

### Relocate Taking Too Long

**Check:** Monitor progression for `RunningFinalSync` status.

```bash
kubectl get drpc myapp-drpc -n myapp -o jsonpath='{.status.progression}'
kubectl get drpc myapp-drpc -n myapp -o jsonpath='{.status.lastGroupSyncBytes}'
```

**Common causes:** Large data sync in progress or source cluster not
accessible.

**Solution:** Wait for final sync to complete. If source cluster is down,
use Failover instead of Relocate.

### PeerReady Condition False

**Check:** Verify peer cluster connectivity and VRG status.

```bash
kubectl get managedcluster west-cluster
kubectl get vrg myapp-drpc -n myapp --context west-cluster
```

**Common causes:** Peer cluster unavailable or storage replication not
established.

**Solution:** Ensure peer cluster is healthy and replication is configured
correctly.

### Cannot Delete DRPC

**Check:** Look for stuck finalizers or VRG cleanup issues.

```bash
kubectl get drpc myapp-drpc -n myapp -o jsonpath='{.metadata.finalizers}'
kubectl get vrg -n myapp --context east-cluster
```

**Common causes:** VRG cleanup pending on managed clusters.

**Solution:** Wait for VRG cleanup. If stuck, manually delete VRG or remove
finalizers (use with caution).

## Best Practices

1. **Use descriptive names** - Include app name in DRPC name (e.g.,
   `myapp-drpc`)

1. **Label PVCs specifically** - Use unique labels to avoid protecting wrong
   PVCs

1. **Monitor status regularly** - Check `lastGroupSyncTime` to ensure
   replication is working

1. **Test failover** - Perform planned failover tests before actual disasters

1. **Document procedures** - Create runbooks for DR operations specific to
   your apps

1. **Use GitOps when possible** - For easier application recreation

1. **Set appropriate capture intervals** - Balance protection vs. overhead
    - Critical apps: 5m
    - Standard apps: 15m to 30m
    - Low-priority: 1h

1. **Verify peer readiness** - Check `PeerReady` condition before triggering
   DR

1. **Clean up after testing** - Remove DRPCs for apps that don't need DR

## Related Resources

- [DRPolicy CRD](drpolicy-crd.md) - Defines DR topology referenced by DRPC
- [VolumeReplicationGroup CRD](vrg-crd.md) - Created by DRPC on managed
  clusters
- [Usage Guide](usage.md) - How to protect workloads with Ramen
- [Recipe Documentation](recipe.md) - For Recipe-based protection
