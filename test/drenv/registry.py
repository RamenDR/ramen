# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
Registry cache for container images.

The registry cache uses Docker Distribution registry in pull-through cache mode.
Each upstream registry gets its own cache instance on a different port.
This speeds up image pulls and reduces network traffic.
"""

import hashlib
import logging

from drenv import commands

# Label used to store configuration hash on containers.
CONFIG_LABEL = "DrenvConfig"

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


def cache_running():
    """Return True if all registry cache containers are running."""
    if not _podman_available():
        return False

    return all(_cache_running(reg) for reg in REGISTRIES)


def setup():
    """Start all registry cache containers if not already running."""
    _require_podman()

    for registry, config in REGISTRIES.items():
        name = _container_name(registry)
        cmd, config_hash = _container_command(name, config)

        if _container_exists(name):
            if _container_is_current(name, config_hash):
                logging.debug("[registry] Cache for %s is current", registry)
                continue

            _remove_container(name)

        _create_container(name, config, cmd)


def cleanup():
    """Stop and remove all registry cache containers."""
    if not _podman_available():
        logging.info("[registry] Podman is not available, skipping cleanup")
        return

    for registry in REGISTRIES:
        name = _container_name(registry)
        if _container_exists(name):
            _remove_container(name)


def _cache_running(registry):
    """Return True if the registry cache container is running."""
    name = _container_name(registry)
    return _container_exists(name) and _container_running(name)


def _container_name(registry):
    """Return the container name for a registry cache."""
    return f"drenv-cache-{registry.replace('.', '-')}"


def _podman_available():
    """
    Return True if podman is available.

    On macOS, podman requires a running podman machine. On Linux, podman
    runs natively.
    """
    try:
        commands.run("podman", "info")
        return True
    except commands.Error:
        return False


def _require_podman():
    """
    Raise if podman is not available.

    Lets podman's own error message propagate, which includes helpful hints
    like "try `podman machine start`" on macOS.
    """
    commands.run("podman", "info")


def _container_exists(name):
    """
    Return True if the container exists.

    Podman exit codes:
      0: containers exist
      1: not found
      125: storage error
    """
    try:
        commands.run("podman", "container", "exists", name)
        return True
    except commands.Error as e:
        if e.exitcode == 1:
            return False
        raise


def _container_running(name):
    """
    Return True if the container is running. The container must exist.

    Uses podman inspect to query the container state.
    """
    out = commands.run("podman", "inspect", "--format", "{{.State.Running}}", name)
    return out.strip() == "true"


def _create_container(name, config, cmd):
    """Create and start a registry cache container."""
    container_id = commands.run(*cmd).rstrip()
    logging.info(
        "[registry] Created container %s on port %s (id: %s)",
        name,
        config["port"],
        container_id,
    )


def _remove_container(name):
    """
    Stop and remove a container, ignoring missing container.
    """
    commands.run("podman", "rm", "--force", name)
    logging.info("[registry] Removed container %s", name)


def _container_is_current(name, config_hash):
    """
    Return True if the container is running with the given config hash.
    The container must exist.
    """
    return _container_running(name) and _container_config_hash(name) == config_hash


def _container_config_hash(name):
    """
    Return the config hash label from a container. The container must exist.

    Uses podman inspect to read the DrenvConfig label value.
    """
    out = commands.run(
        "podman",
        "inspect",
        "--format",
        "{{.Config.Labels." + CONFIG_LABEL + "}}",
        name,
    )
    return out.strip()


def _config_hash(cmd):
    """
    Return a sha256 hash of the container command.

    The hash covers all arguments (image, ports, volumes, env vars) so any
    configuration change produces a different hash.
    """
    h = hashlib.sha256()
    for arg in cmd:
        h.update(arg.encode())
    return h.hexdigest()


def _container_command(name, config):
    """
    Return (cmd, config_hash) for a registry cache container.

    The command includes a DrenvConfig label with the hash of the command
    arguments. The hash can be compared with _container_config_hash() to
    detect configuration changes.
    """
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
        "--label",
        f"{CONFIG_LABEL}=",
        CACHE_IMAGE,
    ]
    config_hash = _config_hash(cmd)
    cmd[-2] += config_hash
    return cmd, config_hash
