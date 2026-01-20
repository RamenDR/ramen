<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# End to End testing

RamenDR end-to-end (e2e) tests validate various scenarios for regional DR
using predefined workloads and deployment methods.

## Running End to End tests

> [!IMPORTANT]
> All commands must be ran from the e2e directory.

### Preparing a `config.yaml` file

#### For drenv environment

If `drenv` was used to configure RDR clusters, easily copy `config.yaml.sample`
and add the clusters kubeconfig paths into `config.yaml` using:

```sh
cat config.yaml.sample ~/.config/drenv/rdr/config.yaml > config.yaml
```

If the `drenv` tool was used to configure RDR clusters with the KubeVirt addon
for deploying VirtualMachine workloads using the configuration file located at
`envs/regional-dr-kubevirt.yaml`, follow these steps to complete the e2e setup
for VM workload testing:

```sh
cat config-vm.yaml.sample ~/.config/drenv/rdr-kubevirt/config.yaml > config.yaml
```

#### For real cluster

Create a `config.yaml` file by copying the `config.yaml.sample` template:

```sh
cp config.yaml.sample config.yaml
```

Update `config.yaml` by uncommenting and adding cluster kubeconfig paths
for the hub and managed clusters.

```yaml
Clusters:
  hub:
    kubeconfigpath: /path/to/kubeconfig/hub
  c1:
    kubeconfigpath: /path/to/kubeconfig/c1
  c2:
    kubeconfigpath: /path/to/kubeconfig/c2
```

### Validating the clusters

Before running tests it is useful to validate that the clusters are accessible
and ready for testing. You can verify it using:

```sh
./run.sh -test.run TestValidation
```

Example output:

```console
2025-03-06T16:52:54.122+0200    INFO    Using config file "config.yaml"
2025-03-06T16:52:54.122+0200    INFO    Using log file "ramen-e2e.log"
2025-03-06T16:52:54.127+0200    INFO    Using Timeout: 10m0s
2025-03-06T16:52:54.127+0200    INFO    Using RetryInterval: 5s
...
2025-03-06T16:52:54.149+0200    INFO    Ramen hub operator pod "ramen-hub-operator-865bdf6799-bgxkn" is running in cluster "hub"
2025-03-06T16:52:54.152+0200    INFO    Ramen dr cluster operator pod "ramen-dr-cluster-operator-67dff877f5-vntt7" is running in cluster "dr1"
2025-03-06T16:52:54.152+0200    INFO    Ramen dr cluster operator pod "ramen-dr-cluster-operator-67dff877f5-6v5sh" is running in cluster "dr2"
--- PASS: TestValidation (0.00s)
    --- PASS: TestValidation/hub (0.02s)
    --- PASS: TestValidation/c1 (0.03s)
    --- PASS: TestValidation/c2 (0.03s)
PASS
```

Our clusters are ready for testing!

### Running DR tests

To run all the DR tests run the TestDR test:

```sh
./run.sh -test.run TestDR
```

The test perform a full DR flow with a tiny workload with multiple deployemnet
methods and storage configurations.

> [!TIP]
> The tests typically complete in 10 minutes, depending the machine running the tests.

When all tests complete we will see a test summary showing the status of all
tests and the time to complete every step:

```console
--- PASS: TestDR (7.14s)
    --- PASS: TestDR/subscr-deploy-rbd-busybox (533.34s)
        --- PASS: TestDR/subscr-deploy-rbd-busybox/Deploy (10.38s)
        --- PASS: TestDR/subscr-deploy-rbd-busybox/Enable (96.47s)
        --- PASS: TestDR/subscr-deploy-rbd-busybox/Failover (210.97s)
        --- PASS: TestDR/subscr-deploy-rbd-busybox/Relocate (146.22s)
        --- PASS: TestDR/subscr-deploy-rbd-busybox/Disable (61.05s)
        --- PASS: TestDR/subscr-deploy-rbd-busybox/Undeploy (8.24s)
    --- PASS: TestDR/disapp-deploy-rbd-busybox (546.11s)
        --- PASS: TestDR/disapp-deploy-rbd-busybox/Deploy (3.07s)
        --- PASS: TestDR/disapp-deploy-rbd-busybox/Enable (95.33s)
        --- PASS: TestDR/disapp-deploy-rbd-busybox/Failover (207.88s)
        --- PASS: TestDR/disapp-deploy-rbd-busybox/Relocate (176.84s)
        --- PASS: TestDR/disapp-deploy-rbd-busybox/Disable (40.70s)
        --- PASS: TestDR/disapp-deploy-rbd-busybox/Undeploy (22.30s)
    --- PASS: TestDR/subscr-deploy-cephfs-busybox (652.06s)
        --- PASS: TestDR/subscr-deploy-cephfs-busybox/Deploy (5.31s)
        --- PASS: TestDR/subscr-deploy-cephfs-busybox/Enable (187.23s)
        --- PASS: TestDR/subscr-deploy-cephfs-busybox/Failover (146.48s)
        --- PASS: TestDR/subscr-deploy-cephfs-busybox/Relocate (276.36s)
        --- PASS: TestDR/subscr-deploy-cephfs-busybox/Disable (30.44s)
        --- PASS: TestDR/subscr-deploy-cephfs-busybox/Undeploy (6.23s)
    --- PASS: TestDR/appset-deploy-cephfs-busybox (670.70s)
        --- PASS: TestDR/appset-deploy-cephfs-busybox/Deploy (5.40s)
        --- PASS: TestDR/appset-deploy-cephfs-busybox/Enable (126.50s)
        --- PASS: TestDR/appset-deploy-cephfs-busybox/Failover (115.80s)
        --- PASS: TestDR/appset-deploy-cephfs-busybox/Relocate (367.66s)
        --- PASS: TestDR/appset-deploy-cephfs-busybox/Disable (55.21s)
        --- PASS: TestDR/appset-deploy-cephfs-busybox/Undeploy (0.13s)
    --- PASS: TestDR/appset-deploy-rbd-busybox (671.39s)
        --- PASS: TestDR/appset-deploy-rbd-busybox/Deploy (5.41s)
        --- PASS: TestDR/appset-deploy-rbd-busybox/Enable (96.44s)
        --- PASS: TestDR/appset-deploy-rbd-busybox/Failover (266.11s)
        --- PASS: TestDR/appset-deploy-rbd-busybox/Relocate (247.42s)
        --- PASS: TestDR/appset-deploy-rbd-busybox/Disable (55.89s)
        --- PASS: TestDR/appset-deploy-rbd-busybox/Undeploy (0.12s)
    --- PASS: TestDR/disapp-deploy-cephfs-busybox (749.75s)
        --- PASS: TestDR/disapp-deploy-cephfs-busybox/Deploy (3.09s)
        --- PASS: TestDR/disapp-deploy-cephfs-busybox/Enable (185.70s)
        --- PASS: TestDR/disapp-deploy-cephfs-busybox/Failover (181.02s)
        --- PASS: TestDR/disapp-deploy-cephfs-busybox/Relocate (296.31s)
        --- PASS: TestDR/disapp-deploy-cephfs-busybox/Disable (60.28s)
        --- PASS: TestDR/disapp-deploy-cephfs-busybox/Undeploy (23.36s)
PASS
```

All tests completed successfully!

### Tests configuration

The tests are defined in the configuration file. Each test specifies a deployer
name, workload name, and PVCSpec. The PVCSpec and Deployer names should match a
name in the PVCSpecs and Deployers sections of the configuration file.:

```yaml
tests:
  - deployer: appset
    workload: deploy
    pvcspec: rbd
  ...
```

The tests are generated from the configuration as
"TestDR/{deployer}-{workload}-{pvcspec}-busybox".
See [Running DR tests](#running-dr-tests) section for complete test list.

#### Deployers

The deployers section defines the available deployment methods. Each deployer
has a name, type, and description. The type is used to identify the deployer
implementation. There are 3 types available, appset, subscr, and disapp. The
description provides additional context about the deployer.

### Run specific DR tests

Running specific tests is commonly used when debugging a failing test,
developing a new test, or working on a new deployer or workload. It allows to
selectively execute specific tests by matching full test names using regular
expressions, making it easier to focus on specific scenarios.

#### Run a single DR test

Example:

```sh
./run.sh -test.run TestDR/subscr-deploy-rbd-busybox
```

This command runs the specific test for subscription based RBD busybox application.

#### Run DR tests using a specific deployer

Example:

```sh
./run.sh -test.run TestDR/appset
```

This command runs all DR tests related to ApplicationSet, covering both RBD and
CephFS PVC based applications. Useful when focusing on a specific deployer.

#### Run DR tests using a specific storage

Example:

```sh
./run.sh -test.run TestDR/rbd
```

This command runs all DR tests related to RBD PVCs across all deployers. Ideal
for verifying functionality specific to a storage type.

### Using multiple config files

Use this option if you want to maintain multiple configuration files and run
tests using a specific one. Example usage:

```sh
./run.sh -config my_config.yaml
```

### Step-by-step DR workflow testing

For manual testing and debugging, you can run individual DR operations step by step.
This approach is useful when you need to:

- Debug issues at specific stages of the DR workflow
- Manually verify the state between DR operations
- Test partial DR flows for development purposes
- Understand the detailed behavior of each DR operation

> [!IMPORTANT]
> The step-by-step workflow requires manual cleanup if stopped mid-process.
> Always ensure you complete the full workflow or manually clean up resources.

### Prerequisites

Before running step-by-step tests, ensure your clusters are properly configured:

1. **Deploy a channel** pointing to the application repository:

   ```sh
   kubectl apply -k https://github.com/RamenDR/ocm-ramen-samples.git/channel?ref=main --context hub
   ```

1. **Configure your environment** with the e2e config file as described in the
   [configuration section](#preparing-a-configyaml-file).

### Step-by-step workflow

The following example demonstrates a complete DR workflow using a specific test configuration.
Replace the test name with your desired configuration from the available tests.

#### 1. Deploy the application

Deploy your workload to the primary cluster:

```sh
./run.sh -test.run TestDR/subscr-deploy-rbd-busybox/Deploy
```

This operation:

- Deploys a busybox application with RBD storage using subscription deployer
- Creates necessary namespaces and resources
- Waits for the application to be running on the primary cluster

#### 2. Enable DR protection

Enable DR for the deployed application:

```sh
./run.sh -test.run TestDR/subscr-deploy-rbd-busybox/Enable
```

This operation:

- Creates a DRPlacementControl (DRPC) resource
- Configures volume replication for persistent volumes
- Waits for the protection to be established and stable
- Verifies the application is properly protected

#### 3. Perform failover

Failover the application to the secondary cluster:

```sh
./run.sh -test.run TestDR/subscr-deploy-rbd-busybox/Failover
```

This operation:

- Initiates failover action in the DRPC
- Moves the application from primary to secondary cluster
- Restores persistent volumes on the target cluster
- Waits for the application to be running on the secondary cluster

#### 4. Relocate back to primary

Relocate the application back to the primary cluster:

```sh
./run.sh -test.run TestDR/subscr-deploy-rbd-busybox/Relocate
```

This operation:

- Initiates relocate action in the DRPC
- Moves the application from secondary back to primary cluster
- Ensures data synchronization before the move
- Waits for the application to be running on the primary cluster

#### 5. Disable DR protection

Disable DR protection for the application:

```sh
./run.sh -test.run TestDR/subscr-deploy-rbd-busybox/Disable
```

This operation:

- Removes the DRPlacementControl resource
- Cleans up volume replication configurations
- Ensures the application continues running without protection

#### 6. Undeploy the application

Clean up by removing the application:

```sh
./run.sh -test.run TestDR/subscr-deploy-rbd-busybox/Undeploy
```

This operation:

- Removes all application resources
- Cleans up namespaces and persistent volumes
- Ensures complete cleanup of the test environment

### Customizing the workflow

You can adapt this workflow for different configurations by changing the test name:

- **Different deployers**: Replace `subscr` with `appset` or `disapp`
- **Different storage**: Replace `rbd` with `cephfs`
- **Different workloads**: Currently `deploy` is the primary workload available

Examples:

```sh
# ApplicationSet with CephFS storage
./run.sh -test.run TestDR/appset-deploy-cephfs-busybox/Deploy

# Discovered Application with RBD storage
./run.sh -test.run TestDR/disapp-deploy-rbd-busybox/Enable
```

#### Monitoring and debugging

Between each step, you can inspect the cluster state:

```sh
# Check DRPC status
kubectl get drpc -A

# Check VolumeReplication status
kubectl get volumereplication -A

# Check application pods
kubectl get pods -n <app-namespace>

# Check placement decisions
kubectl get placementdecisions -A
```
