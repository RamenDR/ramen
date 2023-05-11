# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import kubectl


def service_url(cluster):
    """
    Find minio service url.
    """
    host_ip = kubectl.get(
        "pod",
        "--selector=component=minio",
        "--namespace=minio",
        "--output=jsonpath={.items[0].status.hostIP}",
        context=cluster,
    )
    service_port = kubectl.get(
        "service/minio",
        "--namespace=minio",
        "--output=jsonpath={.spec.ports[0].nodePort}",
        context=cluster,
    )
    return f"http://{host_ip}:{service_port}"
