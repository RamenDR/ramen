# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
Registry cache for container images.

The registry cache uses Docker Distribution registry in pull-through cache mode.
Each upstream registry gets its own cache instance on a different port.
This speeds up image pulls and reduces network traffic.
"""

import logging

from drenv import commands

CACHE_IMAGE = "docker.io/library/registry:3"

# Each upstream registry maps to a local port.
# The port is used in the containerd hosts.toml configuration.
# Ports start at 5051 to avoid conflict with the local registry (port 5050)
# and macOS AirDrop (port 5000).
REGISTRIES = {
    "quay.io": {"port": 5051, "upstream": "https://quay.io"},
    "docker.io": {"port": 5052, "upstream": "https://registry-1.docker.io"},
    "registry.k8s.io": {"port": 5053, "upstream": "https://registry.k8s.io"},
    "ghcr.io": {"port": 5054, "upstream": "https://ghcr.io"},
    "gcr.io": {"port": 5055, "upstream": "https://gcr.io"},
}


def _container_name(registry):
    """Return the container name for a registry cache."""
    return f"drenv-cache-{registry.replace('.', '-')}"


def _cache_running(registry):
    """Return True if the registry cache container is running."""
    name = _container_name(registry)
    try:
        out = commands.run(
            "podman",
            "inspect",
            "--format",
            "{{.State.Running}}",
            name,
        )
        return out.strip() == "true"
    except commands.Error as e:
        logging.debug("[registry] Cannot inspect %s: %s", name, e)
        return False


def cache_running():
    """Return True if all registry cache containers are running."""
    return all(_cache_running(reg) for reg in REGISTRIES)


def setup():
    """Start all registry cache containers if not already running."""
    for registry, config in REGISTRIES.items():
        if _cache_running(registry):
            logging.debug("[registry] Cache for %s already running", registry)
            continue

        name = _container_name(registry)
        logging.info(
            "[registry] Starting cache for %s on port %s", registry, config["port"]
        )
        cmd = [
            "podman",
            "run",
            "--detach",
            "--name",
            name,
            "--publish",
            f"{config['port']}:5000",
            "--volume",
            f"{name}:/var/lib/registry",
            "--env",
            f"REGISTRY_PROXY_REMOTEURL={config['upstream']}",
            CACHE_IMAGE,
        ]
        for line in commands.watch(*cmd):
            logging.debug("[registry] %s", line)
        logging.info("[registry] Cache for %s started", registry)


def cleanup():
    """Stop and remove all registry cache containers."""
    for registry in REGISTRIES:
        name = _container_name(registry)
        logging.info("[registry] Stopping cache for %s", registry)
        try:
            commands.run("podman", "rm", "--force", name)
        except commands.Error as e:
            logging.debug("[registry] Could not remove %s: %s", name, e)
        logging.info("[registry] Cache for %s stopped", registry)
