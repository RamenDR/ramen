# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import cache as _cache
from drenv import kubectl

from .config import CRDS_CACHE_KEY, OPERATORS_CACHE_KEY, PACKAGE_DIR

NAMESPACE = "olm"


def start(cluster):
    deploy(cluster)
    wait(cluster)


def deploy(cluster):
    print("Deploying olm crds")

    # Using Server-side Apply to avoid this failure:
    #   The CustomResourceDefinition "clusterserviceversions.operators.coreos.com"
    #   is invalid: metadata.annotations: Too long: must have at most 262144 bytes
    # See https://medium.com/pareture/kubectl-install-crd-failed-annotations-too-long-2ebc91b40c7d
    path = _cache.get(str(PACKAGE_DIR / "start-data" / "crds"), CRDS_CACHE_KEY)
    kubectl.apply("--filename", path, "--server-side=true", context=cluster)

    print("Waiting until cdrs are established")
    kubectl.wait(
        "--for=condition=established",
        "--filename",
        path,
        context=cluster,
    )

    print("Deploying olm")
    path = _cache.get(
        str(PACKAGE_DIR / "start-data" / "operators"), OPERATORS_CACHE_KEY
    )
    kubectl.apply("--filename", path, context=cluster)


def wait(cluster):
    for operator in "olm-operator", "catalog-operator":
        print(f"Waiting until {operator} is rolled out")
        kubectl.rollout(
            "status",
            "deploy",
            operator,
            f"--namespace={NAMESPACE}",
            context=cluster,
        )

    print(f"Waiting until csv '{NAMESPACE}/packageserver' exists")
    kubectl.wait(
        "csv/packageserver",
        "--for=create",
        f"--namespace={NAMESPACE}",
        context=cluster,
    )
    print(f"Waiting until csv '{NAMESPACE}/packageserver' succeeds")
    kubectl.wait(
        "csv/packageserver",
        f"--namespace={NAMESPACE}",
        "--for=jsonpath={.status.phase}=Succeeded",
        context=cluster,
    )

    print("Waiting for olm pakcage server rollout")
    kubectl.rollout(
        "status",
        "deploy/packageserver",
        f"--namespace={NAMESPACE}",
        context=cluster,
    )
