# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""Select DNS servers for Minikube, including managed-Mac detection."""

import logging
import platform

from . import networkextension

# Public DNS servers - reliable global infrastructure that is likely to be available:
# - 8.8.8.8: Google Public DNS (https://developers.google.com/speed/public-dns)
# - 1.1.1.1: Cloudflare DNS (https://1.1.1.1/)
SERVERS = ("8.8.8.8", "1.1.1.1")


def servers(profile, dns_mode):
    """Return static DNS servers for this profile, or an empty list for host DNS.

    Auto mode selects static DNS only for VM drivers on managed Macs.
    Explicit static mode is supported for VM drivers on any host.
    """
    is_vm = profile["driver"] in ("kvm2", "vfkit")
    if dns_mode == "auto":
        dns_mode = "static" if is_vm and is_managed_mac(profile) else "host"
    if dns_mode == "static":
        if is_vm:
            return list(SERVERS)
        logging.warning(
            "[%s] static dns mode not supported for driver '%s'",
            profile["name"],
            profile["driver"],
        )
    elif dns_mode != "host":
        raise RuntimeError(f"Invalid dns_mode '{dns_mode}'")
    return []


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
