# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import importlib.resources as resources
import json
import logging
import os
import subprocess
import tempfile
import threading
import time
from functools import partial

from drenv import cluster
from drenv import commands
from drenv import kubeconfig
from drenv import yaml

LIMACTL = "limactl"

# Important lima statuses
RUNNING = "Running"
STOPPED = "Stopped"

# Options ignored by this provider.
# TODO: implement what we can.
UNSUPPORTED_OPTIONS = (
    "addons",
    "containerd",
    "driver",
    "extra_config",
    "feature_gates",
    "network",
    "service_cluster_ip_range",
)

# limactl delete is racy, trying to access lima.yaml in other clusters and
# fails when the files are deleted by another limactl process. Until limactl is
# fixed, ensure only single concurent delete.
_delete_vm_lock = threading.Lock()

# Provider scope


def setup():
    pass


def cleanup():
    pass


# Cluster scope


def exists(profile):
    names = _run("list", "--format", "{{.Name}}", context=profile["name"])
    for line in names.splitlines():
        if line == profile["name"]:
            return True
    return False


def start(profile, verbose=False):
    start = time.monotonic()
    logging.info("[%s] Starting lima cluster", profile["name"])

    if not exists(profile):
        _log_unsupported_options(profile)
        with tempfile.NamedTemporaryFile(
            prefix=f"drenv.lima.{profile['name']}.tmp",
        ) as tmp:
            _write_config(profile, tmp.name)
            _create_vm(profile, tmp.name)

    # Get vm before starting to detect a stopped vm.
    vm = _get_vm(profile)

    _start_vm(profile)
    _add_kubeconfig(profile, vm)

    debug = partial(logging.debug, f"[{profile['name']}] %s")
    cluster.wait_until_ready(profile["name"], timeout=30, log=debug)

    if vm["status"] == STOPPED:
        # We have random failures (e.g. ocm webooks) when starting a stopped
        # cluster. Until we find A better way, try to wait give the system
        # more time to become stable.
        # TODO: find a better way.
        time.sleep(15)

    logging.info(
        "[%s] Cluster started in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )


def configure(profile, existing=False):
    # Cluster is configured when created.
    pass


def stop(profile):
    start = time.monotonic()
    logging.info("[%s] Stopping lima cluster", profile["name"])

    # Stop is not idempotent, and using stop -f does not shutdown the guest
    # cleanly, resulting in failures on the next start.
    vm = _get_vm(profile)
    if vm["status"] == RUNNING:
        _stop_vm(profile)

    _remove_kubeconfig(profile)

    logging.info(
        "[%s] Cluster stopped in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )


def delete(profile):
    start = time.monotonic()
    logging.info("[%s] Deleting lima cluster", profile["name"])

    with _delete_vm_lock:
        _delete_vm(profile)

    _delete_additional_disks(profile)
    _remove_kubeconfig(profile)

    logging.info(
        "[%s] Cluster deleted in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )


def load(profile, image):
    start = time.monotonic()
    logging.info("[%s] Loading image", profile["name"])
    with open(image) as f:
        _watch(
            "shell",
            profile["name"],
            "sudo",
            "nerdctl",
            "--namespace=k8s.io",
            "load",
            stdin=f,
            context=profile["name"],
        )
    logging.info(
        "[%s] Image loaded in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )


# Private helpers


def _log_unsupported_options(profile):
    for option in UNSUPPORTED_OPTIONS:
        if profile[option]:
            logging.debug(
                "[%s] Ignoring '%s' for lima cluster",
                profile["name"],
                option,
            )


def _write_config(profile, path):
    """
    Create vm config for profile at path.
    """
    with resources.files("drenv.providers.lima").joinpath("k8s.yaml").open() as f:
        config = yaml.safe_load(f)

    # The "vz" type is required to support amd64 images on arm64, needed for
    # OCM, and also provide the best performance.
    config["vmType"] = "vz"
    config["rosetta"] = {"enabled": True, "binfmt": True}

    # We always use socket_vmnet to get shared network.
    config["networks"] = [{"socket": "/var/run/socket_vmnet"}]

    # Add profile options to template

    config["cpus"] = profile["cpus"]
    config["memory"] = profile["memory"]
    config["disk"] = profile["disk_size"]

    config["additionalDisks"] = _create_additional_disks(profile)

    with open(path, "w") as f:
        yaml.dump(config, f)


def _create_additional_disks(profile):
    disks = _list_disks(profile)
    for disk in disks:
        logging.info("[%s] Creating disk '%s'", profile["name"], disk["name"])
        _create_disk(profile, disk)
    return disks


def _delete_additional_disks(profile):
    for disk in _list_disks(profile):
        logging.info("[%s] Deleting disk %s", profile["name"], disk["name"])
        try:
            _delete_disk(profile, disk)
        except commands.Error as e:
            logging.warning(
                "[%s] Cannot delete disk '%s': %s",
                profile["name"],
                disk["name"],
                e,
            )


def _get_vm(profile):
    out = _run("list", "--format", "json", context=profile["name"])
    for line in out.splitlines():
        vm = json.loads(line)
        if vm["name"] == profile["name"]:
            return vm
    return None


def _list_disks(profile):
    disks = []
    for i in range(profile["extra_disks"]):
        disks.append({"name": f"{profile['name']}-disk{i}", "format": False})
    return disks


def _add_kubeconfig(profile, vm):
    logging.debug("[%s] Adding lima cluster kubeconfig", profile["name"])
    src = os.path.join(vm["dir"], "copied-from-guest", "kubeconfig.yaml")
    _fixup_kubeconfig(profile, src)
    kubeconfig.merge(profile, src)


def _fixup_kubeconfig(profile, path):
    with open(path) as f:
        config = yaml.safe_load(f)

    config["clusters"][0]["name"] = profile["name"]
    config["users"][0]["name"] = profile["name"]

    item = config["contexts"][0]
    item["name"] = profile["name"]
    item["context"]["cluster"] = profile["name"]
    item["context"]["user"] = profile["name"]

    config["current-context"] = profile["name"]

    with open(path, "w") as f:
        yaml.dump(config, f)


def _remove_kubeconfig(profile):
    logging.debug("[%s] Removing lima cluster kubeconfig", profile["name"])
    kubeconfig.remove(profile)


def _create_vm(profile, config):
    _watch("create", "--name", profile["name"], config, context=profile["name"])


def _start_vm(profile):
    _watch("start", profile["name"], context=profile["name"])


def _stop_vm(profile):
    _watch("stop", profile["name"], context=profile["name"])


def _delete_vm(profile):
    # --force allows deletion of a running vm.
    _watch("delete", "--force", profile["name"], context=profile["name"])


def _create_disk(profile, disk):
    _watch(
        "disk",
        "create",
        disk["name"],
        "--format",
        "raw",
        "--size",
        profile["disk_size"],
        context=profile["name"],
    )


def _delete_disk(profile, disk):
    _watch("disk", "delete", disk["name"], context=profile["name"])


def _run(*args, context="lima"):
    cmd = [LIMACTL, *args]
    logging.debug("[%s] Running %s", context, cmd)
    return commands.run(*cmd)


def _watch(*args, stdin=None, context="lima"):
    cmd = [LIMACTL, "--log-format=json", *args]
    logging.debug("[%s] Running %s", context, cmd)
    for line in commands.watch(*cmd, stdin=stdin, stderr=subprocess.STDOUT):
        try:
            info = json.loads(line)
        except ValueError:
            # We don't want to crash if limactl has logging bug, and the line
            # may contain useful info.
            logging.debug("[%s] %s", context, line)
            continue
        info.pop("time", None)
        msg = info.pop("msg", None)
        level = info.pop("level", None)
        log = _loggers.get(level, logging.debug)
        if info:
            log("[%s] %s %s", context, msg, info)
        else:
            log("[%s] %s", context, msg)


# Map lima log levels to python logging functions. Limactl logs are very noisy
# so turn down logging level.
_loggers = {
    "debug": logging.debug,
    "info": logging.debug,
    "warning": logging.debug,
    "error": logging.warning,
    "fatal": logging.error,
}
