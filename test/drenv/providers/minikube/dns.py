# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""Detect managed Macs that need Minikube\'s --dns-servers startup option."""

import logging
import platform

from . import networkextension

# Public DNS servers - reliable global infrastructure that is likely to be available:
# - 8.8.8.8: Google Public DNS (https://developers.google.com/speed/public-dns)
# - 1.1.1.1: Cloudflare DNS (https://1.1.1.1/)
SERVERS = ("8.8.8.8", "1.1.1.1")


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
