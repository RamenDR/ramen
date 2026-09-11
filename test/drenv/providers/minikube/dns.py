# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
DNS bypass configuration for minikube when running on managed Macs.

## The problem

On managed Macs, corporate security agents (e.g., Cisco Umbrella, AnyConnect,
CrowdStrike) install network extensions that implement a hardened DNS proxy.

DNS traffic from the minikube VM bridge (192.168.105.0/24) to the host's DNS
resolver is silently discarded, causing DNS lookups to fail with "Connection
refused" or timeout errors.

However, DNS traffic to public servers (e.g., 8.8.8.8) is forwarded via NAT
normally.

## The solution

We bypass the host's DNS by configuring the VM to use public DNS servers
directly. This module selects the servers; minikube handles their configuration.

This module provides:
- is_managed_mac() - detect if we are running on a managed Mac.
- servers() - return DNS servers for minikube.
"""

import logging
import platform

from . import networkextension

# Public DNS servers - reliable global infrastructure that is likely to be available:
# - 8.8.8.8: Google Public DNS (https://developers.google.com/speed/public-dns)
# - 1.1.1.1: Cloudflare DNS (https://1.1.1.1/)
SERVERS = ("8.8.8.8", "1.1.1.1")


def servers(profile, dns_mode):
    """
    Return cluster DNS servers.

    In static mode, returns public DNS servers for the VM, bypassing the host's
    DNS. In host mode, returns an empty list to use minikube's default DNS
    configuration.

    On non-VM drivers (e.g., docker), static mode is not supported and an empty
    list is returned.

    Arguments:
        profile: Minikube profile dict with "name" and "driver" keys.
        dns_mode: "auto", "static", or "host".
            - auto: Use static on VM drivers with managed Mac, else host.
            - static: Return public DNS servers (8.8.8.8, 1.1.1.1).
            - host: Use minikube's default DNS (may fail on managed Macs).
    """
    is_vm = profile["driver"] in ("kvm2", "vfkit")

    if dns_mode == "auto":
        dns_mode = "static" if is_vm and is_managed_mac(profile) else "host"

    if dns_mode == "static" and not is_vm:
        logging.warning(
            "[%s] static dns mode not supported for driver '%s'",
            profile["name"],
            profile["driver"],
        )
        dns_mode = "host"

    if dns_mode == "host":
        logging.debug("[%s] Using host dns mode", profile["name"])
        return []
    elif dns_mode == "static":
        logging.debug("[%s] Using static dns mode", profile["name"])
        return list(SERVERS)
    else:
        raise RuntimeError(f"Invalid dns_mode '{dns_mode}'")


def is_managed_mac(profile, provider=networkextension):
    """
    Return True if running on macOS with an enabled+active network extension
    (e.g. VPN).

    provider must have list_extensions() returning a list of NetworkExtension,
    default is the real networkextension module.
    """
    if platform.system() != "Darwin":
        return False
    for ext in provider.list_extensions():
        if ext.enabled and ext.active:
            logging.debug(
                "[%s] Detected enabled and active network extension: %s",
                profile["name"],
                ext,
            )
            return True
    return False
