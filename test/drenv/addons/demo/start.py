# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import time
from pathlib import Path

from drenv import commands
from drenv import kubectl

PACKAGE_DIR = Path(__file__).parent

_DATA_DIR = str(PACKAGE_DIR / "start-data")


def start(cluster):
    """
    Deploy the demo app with ingress and wait until ready.
    """
    print("Deploying demo")
    kubectl.apply("--filename", f"{_DATA_DIR}/deployment.yaml", context=cluster)

    print("Creating the service")
    kubectl.apply("--filename", f"{_DATA_DIR}/service.yaml", context=cluster)

    print("Waiting until deployment is rolled out")
    kubectl.rollout(
        "status",
        "deployment",
        timeout=120,
        context=cluster,
    )

    print("Waiting until ingress controller deployment is rolled out")
    kubectl.rollout(
        "status",
        "ingress",
        "--namespace=ingress-nginx",
        timeout=120,
        context=cluster,
    )

    # This can fail for 30 seconds before the webhook is available, but we
    # don't have a way to wait for the webbook.
    start_time = time.monotonic()
    deadline = start_time + 60
    delay = 1

    while True:
        print("Configuring ingress")
        try:
            kubectl.apply("--filename", f"{_DATA_DIR}/ingress.yaml", context=cluster)
        except commands.Error as e:
            if "failed to call webhook" not in e.error or time.monotonic() > deadline:
                raise

            print(f"Deploying failed: {e}")
            print(f"Retrying in {delay} seconds")
            time.sleep(delay)
            delay = min(delay * 2, 16)
        else:
            print(f"Ingress configured in {time.monotonic() - start_time:.3f} seconds")
            break

    print("Waiting until ingress 'demo-ingress' has a load balancer address")
    # It can take 40 seconds until the ingress gets an ingress ip address.
    kubectl.wait(
        "ingress/demo-ingress",
        "--for=jsonpath={.status.loadBalancer.ingress}",
        timeout=120,
        context=cluster,
    )
