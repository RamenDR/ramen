<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Delete test

Tests deleting an application during actions.

Notes:

- Currently testing only delete during failover "Cleaning Up" progression

## Running with minikube

```
env=$PWD/test/envs/regional-dr.yaml
delete-test/setup $env
delete-test/test $env
```

Since ramen is broken now, this test always fails and requires manual
inspection and cleanup.

Check the clusters manually using:

```
watch kubectl get deploy,pod,pvc,vrg,vr --namespace busybox-sample --context {dr1,dr2}
```

### Manual cleanup

On the failed cluster disable mirroring on the image and delete the vr:

```
kubectl rook-ceph --context dr1 rbd mirror pool status -p replicapool --verbose
kubectl rook-ceph --context dr1 rbd mirror image disable --force {image-name} -p replicapool
kubectl delete vr busybox-pvc --namespace busybox-sample --context dr1
```

On the failover cluster delete the application PVC:

```
kubectl delete pvc busybox-pvc --namespace busybox-sample --context dr2
```

Finally run cleanup to remove all resources add by the test:

```
delete-test/cleanup $env
```

## Running on OpenShift

Use oc login to import the cluster config into the same kube config:

```
for url in {hub-url} {cluster1-url} {cluster2-url}; do
    oc login {url} --username {user} --password {pass} --kubeconfig kube/config
done
```

Fix `kube/config` context names manually if needed (you may get very
long context names).

Export the kubeconfig for running with the clusters:

```
export KUBECONFIG=$PWD/kube/config
```

Create env file for the clusterset:

```
$ cat ocp.yaml
ramen:
  hub: perf1
  clusters: [perf2, perf3]
  topology: regional-dr
```

Create the application and assign drpolicy using the UI.

If you are not using `busybox-sample` for app name and namespace, update
the test config.yaml:

```
name: my-app-name
namespace: my-app-namespace
```

To test run:

```
env=$PWD/ocp.yaml
delete-test/setup $env
delete-test/test $env
```

Manual inspection and cleanup is the same as in minikube.
