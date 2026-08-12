# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import json
import os

import drenv
from drenv import cluster as drenv_cluster
from drenv import kubectl
from drenv import subctl

VERSION = "devel"

NAMESPACE = "submariner-operator"

BROKER_DEPLOYMENTS = ("submariner-operator",)

CLUSTER_DEPLOYMENTS = (
    "submariner-operator",
    "submariner-lighthouse-agent",
    "submariner-lighthouse-coredns",
)


def start(broker, *clusters):
    for cluster in [broker, *clusters]:
        drenv_cluster.wait_until_ready(cluster)

    broker_info = deploy_broker(broker)

    for cluster in clusters:
        join_cluster(cluster, broker_info)

    for cluster in clusters:
        configure_submariner(cluster)

    for cluster in clusters:
        wait_for_cluster(cluster)

    for cluster in clusters:
        restart_coredns(cluster)

    for cluster in clusters:
        wait_for_coredns(cluster)


def deploy_broker(broker):
    print(f"Waiting until broker '{broker}' is ready")
    drenv_cluster.wait_until_ready(broker)

    broker_dir = os.path.join(drenv.config_dir(broker), "submariner")
    broker_info = os.path.join(broker_dir, subctl.BROKER_INFO)

    print(f"Creating submariner configuration directory '{broker_dir}'")
    os.makedirs(broker_dir, exist_ok=True)

    print(f"Deploying submariner broker in cluster '{broker}'")
    subctl.deploy_broker(
        broker,
        globalnet=True,
        broker_info=broker_info,
        version=VERSION,
    )
    print(f"Broker info stored in '{broker_info}'")

    print(f"Waiting for submariner broker deployments in cluster '{broker}'")
    wait_for_deployments(broker, BROKER_DEPLOYMENTS, NAMESPACE)

    return broker_info


def join_cluster(cluster, broker_info):
    print(f"Waiting until cluster '{cluster}' is ready")
    drenv_cluster.wait_until_ready(cluster)

    print(f"Annotating nodes in '{cluster}'")
    annotate_nodes(cluster)

    print(f"Joining cluster '{cluster}' to broker")
    subctl.join(
        broker_info,
        context=cluster,
        clusterid=cluster,
        cable_driver="vxlan",
        version=VERSION,
    )


def configure_submariner(cluster):
    """
    Configure submariner for minikube kernel.

    TODO: Remove when minikube supports knftables:
    https://github.com/kubernetes/minikube/issues/23450
    """
    print(f"Configuring submariner on cluster '{cluster}'")
    configmap = """
apiVersion: v1
kind: ConfigMap
metadata:
  name: submariner-global
  namespace: submariner-operator
data:
  use-nftables: "false"
"""
    kubectl.apply("--filename=-", input=configmap, context=cluster)


def annotate_nodes(cluster):
    """
    Annotate all nodes with the gateway public IP address. Required when is
    having multiple interfaces and some networks are not shared (e.g. lima user
    network).
    """
    out = kubectl.get("node", "--output=json", context=cluster)
    nodes = json.loads(out)
    for node in nodes["items"]:
        for item in node["status"]["addresses"]:
            if item["type"] == "InternalIP":
                break
        else:
            raise RuntimeError(f"Cannot find node '{node['metadata']['name']}' address")
        print(f"Annotating '{node['metadata']['name']}' address '{item['address']}'")
        kubectl.annotate(
            f"node/{node['metadata']['name']}",
            {"gateway.submariner.io/public-ip": f"ipv4:{item['address']}"},
            overwrite=True,
            context=cluster,
        )


def wait_for_cluster(cluster):
    print(f"Waiting for submariner deployuments in cluster '{cluster}'")
    wait_for_deployments(cluster, CLUSTER_DEPLOYMENTS, NAMESPACE)


def restart_coredns(cluster):
    """
    Reload cluster CoreDNS after lighthouse has patched the Corefile.

    CoreDNS reload defaults to ~30s; until then clusterset.local queries miss
    the lighthouse forward and NXDOMAIN is cached.
    """
    print(f"Restarting CoreDNS on cluster '{cluster}'")
    kubectl.rollout(
        "restart",
        "deployment/coredns",
        "--namespace=kube-system",
        context=cluster,
    )


def wait_for_coredns(cluster):
    print(f"Waiting for CoreDNS on cluster '{cluster}'")
    kubectl.rollout(
        "status",
        "deployment/coredns",
        "--namespace=kube-system",
        timeout=60,
        context=cluster,
    )


def wait_for_deployments(cluster, names, namespace):
    for name in names:
        deployment = f"deploy/{name}"
        print(
            f"Waiting until deployment '{namespace}/{name}' exists in cluster '{cluster}'"
        )
        kubectl.wait(
            deployment, "--for=create", f"--namespace={namespace}", context=cluster
        )

        print(
            f"Waiting until deployment '{namespace}/{name}' is rolled out in cluster '{cluster}'"
        )
        kubectl.rollout(
            "status",
            deployment,
            f"--namespace={namespace}",
            timeout=180,
            context=cluster,
        )
