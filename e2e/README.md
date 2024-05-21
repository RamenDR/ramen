<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# e2e-rdr

e2e-rdr.sh is a shell script that runs the RamenDR end to end test for regional DR.

RamenDR end to end test is written in Go test, and uses config.yaml as config file.

## e2e test framework

Currently RamenDR end to end test uses an exhaustive suite test,
running related actions for defined workload and deployer combinations.

Exhaustive suite test uses 3 basic concepts:

- workload
    - deployment
- deployer
    - subscription
    - applicationset
- actions
    - deploy
    - enable
    - failover
    - relocate
    - disable
    - undeploy

Workload refers to the kind of application to be deployed in the cluster,
such as deployment, daemonset, statefulset. Only deployment is currently
supported.

Deployer refers to how to deploy the application into the cluster. Only
subscription and applicationset are currently supported.

For workload related actions, currently supported actions are:

- deploy: deploy workload in the cluster
- enable: enable workload to be protected by RamenDR
- failover: failover workload from one cluster to the other
- relocate: relocate workload from one cluster to the other
- disable: remove workload from protection by RamenDR
- undeploy: remove workload from the cluster

## config.yaml

By default the config.yaml contains following configs:

```
channelname: "ramen-gitops"
channelnamespace: "ramen-samples"
giturl: "https://github.com/RamenDR/ocm-ramen-samples.git"
pvcspecs:
  - storageclassname: rook-cephfs
    accessmodes: ReadWriteMany
  - storageclassname: rook-ceph-block
    accessmodes: ReadWriteOnce
```

`channelname`, `channelnamespace`, `giturl` are used to create a channel CR
(channels.apps.open-cluster-management.io) with the given name of
"ramen-gitops" in "ramen-samples" namespace, pointing to the given git url.
The channel CR is later used by subscription CR.

`giturl` is also used by applicationset CR.

Deployment uses below two fixed values, and git url defined above,
pointing to an application in the git url to be deployed into the cluster:

```
GITPATH     = "workloads/deployment/base"
GITREVISION = "main"
```

`pvcspecs` are used to kustomize the workload to use different pvc parameters
`storageclassname` and `accessmodes`.

Each pvc spec in the list of `pvcspecs` will be applied to each workload. By
default it will creates two deployments with different pvc parameters, one with
CephFS and RWX, and the other with RBD and RWO. Since CephFS is the first in
`pvcspecs` list, the deployment using CephFS will be auto named as
`Deployment-0` in the test case name, and the deployment using RBD will be auto
named as `Deployment-1` in the test case name.

## run e2e-test

To Run e2e-test, we need add kubeconfig info into the config.yaml,
with format below:

```
Clusters:
  c1:
    kubeconfigpath: /home/par/.config/drenv/rdr-rdr/kubeconfigs/rdr-dr1
  c2:
    kubeconfigpath: /home/par/.config/drenv/rdr-rdr/kubeconfigs/rdr-dr2
  hub:
    kubeconfigpath: /home/par/.config/drenv/rdr-rdr/kubeconfigs/rdr-hub
```

If you use drenv to setup 3 minikube clusters with `rdr-` prefix, you could
easily add the 3 minikube clusters kubeconfig info into e2e config.yaml by:

```
cat ~/.config/drenv/rdr-rdr/config.yaml >> config.yaml
```

After kubeconfig info is added, to run all the tests, just run:

`./e2e-rdr.sh`

## run individual e2e test case

It is also ok to use below command to run individual test case:

`./e2e-rdr.sh -run TestName`

TestName list is:

```
TestSuites/Exhaustive/Deployment-0/Subscription/Deploy
TestSuites/Exhaustive/Deployment-0/Subscription/Enable
TestSuites/Exhaustive/Deployment-0/Subscription/Failover
TestSuites/Exhaustive/Deployment-0/Subscription/Relocate
TestSuites/Exhaustive/Deployment-0/Subscription/Disable
TestSuites/Exhaustive/Deployment-0/Subscription/Undeploy

TestSuites/Exhaustive/Deployment-1/Subscription/Deploy
TestSuites/Exhaustive/Deployment-1/Subscription/Enable
TestSuites/Exhaustive/Deployment-1/Subscription/Failover
TestSuites/Exhaustive/Deployment-1/Subscription/Relocate
TestSuites/Exhaustive/Deployment-1/Subscription/Disable
TestSuites/Exhaustive/Deployment-1/Subscription/Undeploy

TestSuites/Exhaustive/Deployment-0/Appset/Deploy
TestSuites/Exhaustive/Deployment-0/Appset/Enable
TestSuites/Exhaustive/Deployment-0/Appset/Failover
TestSuites/Exhaustive/Deployment-0/Appset/Relocate
TestSuites/Exhaustive/Deployment-0/Appset/Disable
TestSuites/Exhaustive/Deployment-0/Appset/Undeploy

TestSuites/Exhaustive/Deployment-1/Appset/Deploy
TestSuites/Exhaustive/Deployment-1/Appset/Enable
TestSuites/Exhaustive/Deployment-1/Appset/Failover
TestSuites/Exhaustive/Deployment-1/Appset/Relocate
TestSuites/Exhaustive/Deployment-1/Appset/Disable
TestSuites/Exhaustive/Deployment-1/Appset/Undeploy
```

For example, below command will deploy a deployment with name `deployment-0`,
with RBD and RWO pvc, using ApplicationSet CR.

`./e2e-rdr.sh -run TestSuites/Exhaustive/Deployment-0/Appset/Deploy`