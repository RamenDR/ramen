# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from pathlib import Path

from drenv import kubectl

PACKAGE_DIR = Path(__file__).parent
DEPLOYMENT = str(PACKAGE_DIR / "start-data" / "deployment.yaml")


def start(cluster):
    """
    Deploy the example app and wait until it is ready.
    """
    print("Deploying example")
    kubectl.apply("--filename", DEPLOYMENT, context=cluster)

    print("Waiting until example deployment is rolled out")
    kubectl.rollout(
        "status",
        "deploy/example-deployment",
        timeout=180,
        context=cluster,
    )

    print("waiting until pod is ready")
    kubectl.wait(
        "pod",
        "--selector=app=example",
        "--for=condition=Ready",
        timeout=180,
        context=cluster,
    )
