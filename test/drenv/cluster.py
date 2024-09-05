# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import json
import time

from . import kubectl
from . import commands

# Cluster does not have kubeconfig.
UNKNOWN = "unknwon"

# Cluster has kubeconfig.
CONFIGURED = "configured"

# APIServer is ready.
READY = "ready"


def status(name):
    if not kubeconfig(name):
        return UNKNOWN

    try:
        readyz(name)
    except commands.Error:
        return CONFIGURED

    return READY


def wait_until_ready(name, timeout=600, log=print):
    """
    Wait until a cluster is ready.

    This is useful when starting profiles concurrently, when one profile needs
    to wait for another profile, or when restarting a stopped cluster.
    """
    log(f"Waiting until cluster '{name}' is ready")
    deadline = time.monotonic() + timeout
    delay = min(1.0, timeout / 60)
    last_status = None

    while True:
        current_status = status(name)

        if current_status != last_status:
            log(f"Cluster '{name}' is {current_status}")
            last_status = current_status

        if current_status == READY:
            break

        if time.monotonic() > deadline:
            raise RuntimeError(f"Timeout waiting for cluster '{name}'")

        time.sleep(delay)


def kubeconfig(context_name):
    """
    Return cluster kubeconfig. Returns empty dict if the cluster is not
    configured with kubectl yet.
    """
    out = kubectl.config("view", "--output", "json")
    config = json.loads(out)

    # We get null instead of [].
    clusters = config.get("clusters") or ()
    contexts = config.get("contexts") or ()

    # On external cluster, the name of the context many be differnet from the
    # name of the cluster.
    for context in contexts:
        if context["name"] == context_name:
            cluster_name = context["context"]["cluster"]
            for cluster in clusters:
                if cluster["name"] == cluster_name:
                    return cluster

    return {}


def readyz(name, verbose=False):
    """
    Check if API server is ready.
    https://kubernetes.io/docs/reference/using-api/health-checks/
    """
    path = "/readyz"
    if verbose:
        path += "?verbose"
    return kubectl.get("--raw", path, context=name)
