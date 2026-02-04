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
directly. In static mode, we configure systemd-resolved in the minikube VM
with two settings:

1. DNS servers (resolvectl dns eth0 8.8.8.8 1.1.1.1):
   Sets public DNS servers that are reachable from the VM, bypassing the
   host's broken DNS path.

2. Routing domain (resolvectl domain eth0 "~."):
   The "~." syntax tells systemd-resolved to route ALL DNS queries through
   eth0's DNS servers. The "~" prefix marks it as a routing domain (not a
   search domain), and "." matches the root domain (everything). Without
   this, systemd-resolved might still try other interfaces' DNS servers.

These resolvectl commands configure the runtime state, but DHCP lease renewal
would overwrite our settings. To make them persistent, we create a systemd
network override file (/etc/systemd/network/00-static-dns.network):

- [Match] Name=eth*: Apply to all ethernet interfaces.
- [Network] DHCP=yes: Continue using DHCP for IP address.
- [Network] DNS=8.8.8.8 1.1.1.1: Override DNS servers.
- [DHCP] UseDNS=false: Ignore DNS servers provided by DHCP.

The "00-" prefix ensures this file is processed before minikube's default
network configuration (20-dhcp.network).

This module provides:
- is_managed_mac() - detect if we are running on a managed Mac.
- configure() - configure minikube DNS.
"""

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


def configure(provider, profile, dns_mode):
    """
    Configure and verify cluster DNS.

    In static mode, configures the VM to use public DNS servers, bypassing
    the host's DNS. In host mode, uses minikube's default DNS configuration.

    After configuration, verifies DNS resolution by querying google.com.
    Fails immediately if DNS is broken, providing early feedback instead of
    obscure failures later.

    On non-VM drivers (e.g., docker), static mode is not supported and
    verification is skipped since resolvectl is not available.

    Arguments:
        provider: Must have ssh(name, script) method.
        profile: Minikube profile dict with "name" and "driver" keys.
        dns_mode: "auto", "static", or "host".
            - auto: Use static on VM drivers with managed Mac, else host.
            - static: Configure public DNS servers (8.8.8.8, 1.1.1.1).
            - host: Use minikube's default DNS (may fail on managed Macs).
    """
    if dns_mode == "auto":
        dns_mode = "static" if _is_vm(profile) and is_managed_mac(profile) else "host"

    if dns_mode == "static" and not _is_vm(profile):
        logging.warning(
            "[%s] static dns mode not supported for driver '%s'",
            profile["name"],
            profile["driver"],
        )
        dns_mode = "host"

    if dns_mode == "host":
        logging.debug("[%s] Using host dns mode", profile["name"])
    elif dns_mode == "static":
        logging.debug("[%s] Using static dns mode", profile["name"])
        _configure_static_dns(provider, profile["name"])
    else:
        raise RuntimeError(f"Invalid dns_mode '{dns_mode}'")

    _verify(provider, profile)


def _configure_static_dns(provider, name, servers=SERVERS):
    nameservers = " ".join(servers)

    script = f"""\
cat > /etc/systemd/network/00-static-dns.network <<EOF
[Match]
Name=eth*
[Network]
DHCP=yes
DNS={nameservers}
[DHCP]
UseDNS=false
EOF
resolvectl dns eth0 {nameservers}
resolvectl domain eth0 "~."
resolvectl flush-caches
"""
    provider.ssh(name, f"sudo bash -o errexit -c '{script}'")


def _verify(provider, profile):
    """
    Verify DNS resolution works on VM-based drivers.

    On non-VM drivers (e.g., docker) we don't configure DNS so there is
    nothing to verify.
    """
    if not _is_vm(profile):
        return

    logging.debug("[%s] Verifying dns resolution", profile["name"])
    provider.ssh(profile["name"], "resolvectl query google.com")


def _is_vm(profile):
    """
    Return True if profile uses a VM driver.
    """
    return profile["driver"] in ("kvm2", "vfkit")
