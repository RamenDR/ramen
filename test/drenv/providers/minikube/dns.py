# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
DNS bypass configuration for minikube when running on managed Macs.

On "Managed" macOS environments, corporate security agents (e.g., Cisco
Umbrella, AnyConnect, CrowdStrike) install Network Content Filters and Socket
Filter Extensions.  These agents hijack DNS traffic on Port 53 and bind the
listener exclusively to the loopback interface (`127.0.0.1`).

Because the Minikube VM resides on a virtual bridge subnet (`192.168.105.0/24`),
it is physically prevented from reaching the Mac's loopback DNS listener. This
results in `Connection refused` or `Timed out` errors for both Kubernetes pods
and the VM node itself.

This module provides:
- is_managed_mac() - detect if we are running on a managed Mac that needs DNS bypass
- configure() - configure minikube for DNS bypass
"""

import logging
import platform

from . import networkextension


def is_managed_mac(provider=networkextension):
    """
    Return True if running on macOS with an enabled+active network extension (e.g. VPN).

    provider must have list_extensions() returning a list of NetworkExtension, default is the
    real networkextension module.
    """
    if platform.system() != "Darwin":
        return False
    for ext in provider.list_extensions():
        if ext.enabled and ext.active:
            logging.debug(
                "[minikube.dns] detected enabled and active network extension: %s", ext
            )
            return True
    return False
