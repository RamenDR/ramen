# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import kubectl


def test(cluster):
    """
    Test the example deployment by running hostname in the pod.
    """
    print("Testing example deployment")
    hostname = kubectl.exec(
        "deploy/example-deployment", "--", "hostname", context=cluster
    ).rstrip()
    if not hostname.startswith("example-deployment-"):
        raise RuntimeError(f"Unexpected hostname: '{hostname}'")
