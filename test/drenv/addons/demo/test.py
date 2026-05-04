# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import subprocess


def test(cluster):
    """
    Test the demo deployment by curling the ingress endpoint.
    """
    print("Testing demo deployment")

    cmd = ["minikube", "ip", "--profile", cluster]
    node_addr = subprocess.check_output(cmd).decode().rstrip()

    cmd = ["curl", "--no-progress-meter", f"http://{node_addr}/"]
    out = subprocess.check_output(cmd).decode().rstrip()

    if "Welcome to nginx!" not in out:
        raise RuntimeError(f"Unexpected output: {out}")
