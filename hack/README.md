<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# hack/

## minikube-ramen.sh

Ramen dr-cluster end-to-end test script

- cluster names are specified with the `cluster_names` variable
    - `cluster1` and `cluster2` by default
- application sample namespace name is specified with the `application_sample_namespace_name`
  variable
    - `default` by default
- takes a list of functions to execute:
    - `deploy` (default) deploys the environment including:
      minikube clusters, rook-ceph, minio s3 stores, ramen
    - `undeploy` undeploys the things deployed by `deploy`
    - `manager_redeploy` rebuilds and redeploys ramen manager
    - `application_sample_deploy` deploys busybox-sample app to specified cluster
    - `application_sample_undeploy` undeploys busybox-sample app from specified cluster
    - `application_sample_vrg_deploy` deploys busybox-sample app vrg to 1st cluster
    - `application_sample_vrg_undeploy` undeploys busybox-sample app vrg from 1st
      cluster

## ocm-minikube-ramen.sh

open-cluster-management Ramen end-to-end test script

- can be run from any directory; writes temporary files to /tmp
- installs some dependencies (e.g. minikube, golang, etc), but not necessarily
  all (e.g. kvm on Linux distributions other than RHEL and Ubuntu)
- hub cluster name is specified with the `hub_cluster_name` variable
    - defaults to `hub`
- managed cluster names are specified with the `spoke_cluster_names` variable
    - `cluster1` and `hub` by default
    - a hub may also be a managed cluster
- takes a list of functions to execute:
    - `deploy` (default) deploys the environment including:
      minikube clusters, ocm, rook-ceph, minio s3 stores, olm, ramen
        - calls `ramen_images_build_and_archive` which builds and deploys ramen
          from the source rooted from the parent directory of the script
            - skips the ramen manager image build if the `skip_ramen_build` variable's
              value is something other than an empty string or `false`
        - calls `ramen_deploy` which deploys ramen hub operator, crds, drpolicy
          and samples channel
    - `application_sample_deploy` deploys the busybox-sample app to 1st managed cluster
       named
    - `application_sample_failover` fails over the busybox-sample app to 2nd managed
       cluster named
    - `application_sample_relocate` relocates the busybox-sample app to 1st managed
       cluster named
    - `application_sample_undeploy` undeploys the busybox-sample app from the cluster
       it was last deployed to
    - `undeploy` undeploys the things deployed by `deploy`
        - calls `ramen_undeploy` which undeploys the things deployed by `ramen_deploy`
        - calls `rook_ceph_undeploy` which deletes the minikube clusters and rook-ceph
          virsh volumes and alone can undeploy the environment quickly by skipping
          component undeployments
    - see source for several other routines to deploy and undeploy various copmonents
      individually
- is designed to be idempotent so that deploy functions can be rerun without having
  to first undeploy
    - one exception to this is image deployment
        - for example, if ramen image changes, it can be deployed by undeploying
          and redeploying ramen
    - tip: if an error is encountered leaving something in a state such that an undeployment
      fails, consider redeploying to get it into a known state

Examples:

```sh
spoke_cluster_names=cluster1\ cluster2 ./ocm-minikube-ramen.sh
spoke_cluster_names=cluster1\ cluster2 ./ocm-minikube-ramen.sh application_sample_deploy
spoke_cluster_names=cluster1\ cluster2 ./ocm-minikube-ramen.sh application_sample_failover
spoke_cluster_names=cluster1\ cluster2 ./ocm-minikube-ramen.sh application_sample_relocate
spoke_cluster_names=cluster1\ cluster2 ./ocm-minikube-ramen.sh application_sample_undeploy
./ocm-minikube-ramen.sh ramen_build_and_archive
spoke_cluster_names=cluster1\ cluster2 ./ocm-minikube-ramen.sh ramen_undeploy
spoke_cluster_names=cluster1\ cluster2 ./ocm-minikube-ramen.sh ramen_deploy
```
