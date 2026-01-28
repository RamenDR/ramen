<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-1.0
-->

# DRPlacementControl CRD

## Overview

The **DRPlacementControl** (DRPC) custom resource is the primary interface
for protecting and managing applications with disaster recovery
capabilities. Created by users in the application namespace on the OCM hub
cluster, it orchestrates:

- Initial deployment of applications to the preferred cluster
- Failover operations to peer clusters during disasters
- Planned relocation between clusters
- Volume replication and kube object protection

The DRPC is the control plane for application DR - it creates and manages
VolumeReplicationGroup (VRG) resources on managed clusters via
ManifestWork, coordinates with OCM Placement, and tracks DR state.

**Lifecycle:** One DRPC per application. Created when enabling DR
protection, modified to trigger DR operations, deleted when removing DR
protection.

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
  name: regional-dr-policy
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

The cluster name where the application should initially run and
return to after relocate.

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

**When to use:** Set this along with `action: Failover`
to trigger a failover.

**Example:**

```yaml
failoverCluster: west-cluster
action: Failover
```

**Note:** For relocate operations, change `preferredCluster` instead.

#### `action` (DRAction)

The DR action to perform: `Failover` or `Relocate`.

**Valid values:**

- `Failover` - Recover application on `failoverCluster`
    (assumes source is down)
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

**State machine:** Set action to trigger operation,
DRPC clears it when complete.

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

**Use case:** Multi-namespace applications or shared
configuration namespaces.

#### `kubeObjectProtection` (KubeObjectProtectionSpec)

Configuration for protecting Kubernetes resources (not just PVCs).

**Fields:**

- `captureInterval` (metav1.Duration) -
    How often to capture kube objects (default: 5m)
- `recipeRef` (RecipeRef) - Reference to Recipe for custom workflows
- `recipeParameters` (map[string][]string) - Parameters for Recipe
- `kubeObjectSelector` (metav1.LabelSelector) -
    Selector for objects to protect

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

The DRPC status provides detailed information about
the DR state and progress.

### `phase` (DRState)

Current state of the DRPC.

**States:**

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

**Example values:**

- `Completed` - Operation finished successfully
- `WaitForReadiness` - Waiting for application to become ready
- `CreatingMW` - Creating ManifestWork for VRG
- `FailingOverToCluster` - Executing failover
- `PreparingFinalSync` - Preparing for final data sync
- `WaitForFencing` - Waiting for storage fencing

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

Time of the most recent successful kube object protection.

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
    name: regional-dr-policy

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
    name: regional-dr-policy
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
    name: regional-dr-policy
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
    name: regional-dr-policy
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
    name: regional-dr-policy
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

Output shows: name, age, preferredCluster,
failoverCluster, desiredState, currentState.

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

### Check VRG on Managed Cluster

```bash
kubectl get vrg -n myapp --context east-cluster
kubectl describe vrg myapp-drpc -n myapp --context east-cluster
```

## Troubleshooting

### DRPC Stuck in Deploying

**Check:**

```bash
kubectl get drpc myapp-drpc -n myapp -o yaml
```

**Common causes:**

1. VRG ManifestWork not created - check `status.progression`
1. PVC selector doesn't match any PVCs
1. Storage classes not found on managed cluster

**Debug:**

```bash
# Check ManifestWork
kubectl get manifestwork -A | grep myapp

# Check PVCs match selector
kubectl get pvc -n myapp -l app=myapp

# Check VRG on managed cluster
kubectl get vrg -n myapp --context east-cluster
```

### Failover Not Progressing

**Check progression:**

```bash
kubectl get drpc myapp-drpc -n myapp -o jsonpath='{.status.progression}'
```

**Common issues:**

- `WaitForFencing` - Storage fencing taking time
- `WaitForReadiness` - Application not becoming ready
- Storage replication not healthy

**Check VRG conditions:**

```bash
kubectl get vrg myapp-drpc -n myapp --context west-cluster -o yaml
```

### Data Not Replicating

**Check sync times:**

```bash
kubectl get drpc myapp-drpc -n myapp -o jsonpath='{.status.lastGroupSyncTime}'
```

**If null or stale:**

1. Check VRG status on source cluster
1. Check VolumeReplication resources
1. Verify storage replication is configured

```bash
kubectl get volumereplication -n myapp --context east-cluster
```

### Cannot Delete DRPC

**Stuck in deletion:**

- VRG may have finalizers
- ManifestWork cleanup pending

**Force cleanup (use carefully):**

```bash
kubectl patch drpc myapp-drpc -n myapp -p '{"metadata":{"finalizers":[]}}' --type=merge
```

## Best Practices

1. **Use descriptive names** - Include app name in DRPC name (e.g., `myapp-drpc`)

1. **Label PVCs specifically** - Use unique labels to
    avoid protecting wrong PVCs

1. **Monitor status regularly** - Check `lastGroupSyncTime`
    to ensure replication is working

1. **Test failover** - Perform planned failover tests
    before actual disasters

1. **Document procedures** - Create runbooks for DR operations
    specific to your apps

1. **Use GitOps when possible** - For easier application recreation

1. **Set appropriate capture intervals** - Balance protection vs. overhead

    - Critical apps: 5m
    - Standard apps: 15m to 30m
    - Low-priority: 1h

1. **Verify peer readiness** - Check `PeerReady` condition before triggering DR

1. **Clean up after testing** - Remove DRPCs for apps that don't need DR

## Related Resources

- [DRPolicy](drpolicy-crd.md) - Defines DR topology referenced by DRPC
- [VolumeReplicationGroup](vrg-crd.md) - Created by DRPC on managed clusters
- [Usage Guide](usage.md) - How to protect workloads with Ramen
- [Recipe Documentation](recipe.md) - For Recipe-based protection
