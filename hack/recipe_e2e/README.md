# Recipe End to End Test Scripts

This directory contains scripts for testing Recipes with two Minikube clusters.
Currently, these scripts are limited to Asynchronous/Regional DR without ACM/OCM.

## Setup

Two Minikube clusters are required. The easiest way to deploy these is with the
`minikube-ramen.sh` scripts: `bash hack/minikube-ramen.sh deploy`. This will create
two clusters with async Rook/Ceph mirroring in KVM storage pools, named `cluster1`
and `cluster2`. Minio will be installed on each cluster, and this will be used for
the Ramen S3 store.

Once this setup is successful, proceed with the instructions below.

## Testing

### First time setup

Velero and several CRDs are required to use Recipes. Run `setup.sh` on both Minikube
clusters to install and configure these requirements.

```bash
# working directory: ramen/hack/recipe_e2e

# setup cluster2
kubectl config use-context cluster2
bash scripts/setup.sh

# setup cluster1
kubectl config use-context cluster1
bash scripts/setup.sh
```

### E2E Tests

#### Busybox Application

The application under test is Busybox. A Busybox application consists of:

1. Deployment
1. PVC

These are created in Namespace `recipe-test`. A PV will be created on-demand for
use with the PVC.

#### Protection of Application

"Protection" means that Ramen has backed up the Kubernetes resources used by the
Application, as defined in the Recipe used by the VRG. The VRG used for Protection
can be found [here](protect/vrg_busybox_primary.yaml), and the Recipe [here](protect/recipe_busybox.yaml).

```bash
# deploy and protect application
bash scripts/protect.sh
```

#### Failover from Cluster1 to Cluster2

At the beginning of this step, the application runs on Cluster1, but not Cluster2.
Cluster1 is "fenced" by setting its VRG to Secondary, then Cluster2 deploys a VRG
as Primary and restores the application as defined in the Recipe Restore Workflow.
The final test is the application running on Cluster2 and not Cluster1. Restore
objects will be present in the S3 store from this sequence.

Detailed steps can be found [here](https://github.com/RamenDR/ramen/blob/main/docs/vrg-usage.md#failover-application-from-cluster1-to-cluster2).

```bash
# failover from cluster1 to cluster2
bash scripts/failover.sh
```

#### Failback from Cluster2 to Cluster1

At the beginning of this step, the application runs on Cluster2, but not Cluster1.
VRGs will be present on both clusters at the beginning; Cluster2's VRG as Primary
and Cluster1's as Secondary. Cluster1's VRG will be recreated as Secondary, and
Cluster2's VRG will be demoted to Secondary to enable a final sync of replicated
data, then Cluster1's VRG will be promoted to Primary and the application will be
restored.

Detailed steps can be found [here](https://github.com/RamenDR/ramen/blob/main/docs/vrg-usage.md#failbackrelocate-application-from-cluster2-to-cluster1)

```bash
# bash failback from cluster2 to cluster1
bash scripts/failback.sh
```

#### Resource Teardown

After the testing is complete, it may be desirable to reset the clusters to a
pre-failover/pre-failback state. This can be done with the `teardown.sh` script.

```bash
# teardown cluster1 resources and clear minio-cluster1 s3 contents
bash scripts/teardown.sh cluster1 minio-cluster1

# teardown cluster2 resources and clear minio-cluster2 s3 contents
bash scripts/teardown.sh cluster2 minio-cluster2
```
