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

CACHE_IMAGE = "docker.io/library/registry:2"

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

# Label used to store configuration hash on containers.
CONFIG_LABEL = "DrenvConfig"


def cache_is_running():
    """
    Return True if all registry cache containers are running.

    This does not check if the containers are running with the current
    configuration.
    """
    return all(_container_is_running(_container_name(reg)) for reg in REGISTRIES)


def setup():
    """
    Ensure that all registry cache containers are running with the current
    configuration.

    If a container is already running with the current configuration, it is
    kept. If the configuration changed, the container is recreated. Cached
    data in volumes is preserved across container recreation.
    """
    for registry, config in REGISTRIES.items():
        name = _container_name(registry)
        args = _container_args(name, config)

        if _container_is_current(name, args):
            logging.debug(
                "[registry] Cache for %s already running with current config", registry
            )
            continue

        _remove_container(name)
        _create_container(name, config, args)


def cleanup():
    """
    Registry cache containers are not removed during cleanup.

    The containers persist across environment runs to maintain the in-memory
    cache and improve performance. They are only recreated when the
    configuration changes (detected via config hash label).
    """
    logging.debug("[registry] Keeping cache containers running")


# Private functions


def _container_name(registry):
    """
    Return the container name for a registry cache.
    """
    return f"drenv-cache-{registry.replace('.', '-')}"


def _container_args(name, config):
    """
    Return the podman run arguments for a registry cache container.

    The config label value is empty and filled in by _add_config_hash after
    computing the hash from these arguments.
    """
    return [
        "podman",
        "run",
        "--detach",
        "--name",
        name,
        "--label",
        f"{CONFIG_LABEL}=",  # Value added by _add_config_hash().
        "--publish",
        f"{config['port']}:5000",
        "--volume",
        f"{name}:/var/lib/registry",
        "--env",
        f"REGISTRY_PROXY_REMOTEURL={config['upstream']}",
        "--env",
        "REGISTRY_HTTP_DEBUG_ADDR=:5001",
        "--env",
        "REGISTRY_HTTP_DEBUG_PROMETHEUS_ENABLED=true",
        CACHE_IMAGE,
    ]


def _current_config_hash(args):
    """
    Return a hash of the current container configuration.
    """
    h = hashlib.sha256()
    for arg in args:
        h.update(arg.encode())
    return h.hexdigest()


def _container_config_hash(name):
    """
    Return the config hash label from a running container, or None.
    """
    try:
        out = commands.run(
            "podman",
            "inspect",
            "--format",
            "{{.Config.Labels." + CONFIG_LABEL + "}}",
            name,
        )
        return out.strip() or None
    except commands.Error:
        return None


def _container_is_running(name):
    """
    Return True if the container is running.
    """
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


def _container_is_current(name, args):
    """
    Return True if the container is running with current configuration.
    """
    if not _container_is_running(name):
        return False
    current_hash = _current_config_hash(args)
    stored_hash = _container_config_hash(name)
    return current_hash == stored_hash


def _remove_container(name):
    """
    Remove a container if it exists.
    """
    try:
        commands.run("podman", "rm", "--force", name)
        logging.info("[registry] Removed container %s", name)
    except commands.Error as e:
        logging.debug("[registry] Could not remove %s: %s", name, e)


def _create_container(name, config, args):
    """
    Create a registry cache container.
    """
    logging.info("[registry] Creating container %s on port %s", name, config["port"])
    config_hash = _current_config_hash(args)
    cmd = _add_config_hash(args, config_hash)
    container_id = commands.run(*cmd).rstrip()
    logging.info("[registry] Created container %s (id: %s)", name, container_id)


def _add_config_hash(args, config_hash):
    """
    Add the config hash to the config label argument.
    """
    prefix = f"{CONFIG_LABEL}="
    return [arg + config_hash if arg == prefix else arg for arg in args]
