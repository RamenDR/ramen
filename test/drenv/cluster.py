# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import json
import time

from . import kubectl

# Cluster does not have kubeconfig.
UNKNOWN = "unknwon"

# Cluster has kubeconfig.
CONFIGURED = "configured"

# APIServer is responding.
READY = "ready"


def status(name):
    if not kubeconfig(name):
        return UNKNOWN

    out = kubectl.version(context=name, output="json")
    version_info = json.loads(out)
    if "serverVersion" not in version_info:
        return CONFIGURED

    return READY


def wait_until_ready(name, timeout=300):
    """
    Wait until a cluster is ready.

    This is useful when starting profiles concurrently, when one profile needs
    to wait for another profile.
    """
    deadline = time.monotonic() + timeout
    delay = min(1.0, timeout / 60)
    last_status = None

    while True:
        current_status = status(name)

        if current_status != last_status:
            print(f"Cluster {name} is {current_status}")
            last_status = current_status

        if current_status == READY:
            break

        if time.monotonic() > deadline:
            raise RuntimeError(f"Timeout waiting for cluster {name}")

        time.sleep(delay)


def kubeconfig(name):
    """
    Return cluster kubeconfig. Returns empty dict if the cluster is not
    configured with kubectl yet.
    """
    out = kubectl.config("view", "--output", "json")
    config = json.loads(out)

    # We get null instead of [].
    clusters = config.get("clusters") or ()

    for c in clusters:
        if c["name"] == name:
            return c

    return {}
