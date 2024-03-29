# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import time
import http
import http.client

from drenv import kubectl


def service_url(cluster):
    """
    Find minio service url.
    """
    host_ip, service_port = _service_info(cluster)
    return f"http://{host_ip}:{service_port}"


def wait_for_service(cluster, timeout=60, delay=2, log=print):
    host_ip, service_port = _service_info(cluster)

    start = time.monotonic()
    deadline = start + timeout

    while not _service_is_alive(host_ip, service_port):
        if time.monotonic() > deadline:
            raise RuntimeError("Timeout waiting for minio liveness")

        log(f"Retrying in {delay} seconds")
        time.sleep(delay)

    elapsed = time.monotonic() - start
    log(f"minio service is available in {elapsed:.2f} seconds")


def _service_info(cluster):
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
    return host_ip, service_port


def _service_is_alive(host_ip, service_port, log=print):
    """
    Based on
    https://min.io/docs/minio/linux/operations/monitoring/healthcheck-probe.html#node-liveness
    """
    conn = http.client.HTTPConnection(host_ip, service_port, timeout=60)
    try:
        conn.request("HEAD", "/minio/health/live")
        r = conn.getresponse()
        if r.status != http.HTTPStatus.OK:
            log(f"Service returned unexpected status code: {r.status}")
            return False

        return True
    except ConnectionRefusedError:
        log("Cannot connect to service")
        return False
    finally:
        conn.close()
