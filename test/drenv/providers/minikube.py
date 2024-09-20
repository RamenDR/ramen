# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import errno
import json
import logging
import os
import sys
import time

from packaging.version import Version

from drenv import commands
from drenv import containerd

EXTRA_CONFIG = [
    # When enabled, tells the Kubelet to pull images one at a time. This slows
    # down concurrent image pulls and cause timeouts when using slow network.
    # Defaults to true becasue it is not safe with docker daemon with version
    # < 1.9 or an Aufs storage backend.
    # https://github.com/kubernetes/kubernetes/issues/10959
    # Speeds up regional-dr start by 20%.
    "kubelet.serialize-image-pulls=false"
]


# Provider scope


def setup():
    """
    Set up minikube to work with drenv. Must be called before starting the
    first cluster.

    To load the configuration you must call configure() after a cluster is
    started.
    """
    version = _version()
    logging.debug("[minikube] Using minikube version %s", version)
    _setup_sysctl(version)
    _setup_systemd_resolved(version)


def cleanup():
    """
    Cleanup files added by setup().
    """
    _cleanup_file(_systemd_resolved_drenv_conf())
    _cleanup_file(_sysctl_drenv_conf())


# Cluster scope


def exists(profile):
    out = _profile("list", output="json")
    profiles = json.loads(out)
    for p in profiles["valid"]:
        if p["Name"] == profile["name"]:
            return True
    return False


def start(profile, verbose=False, timeout=None):
    start = time.monotonic()
    logging.info("[%s] Starting minikube cluster", profile["name"])

    args = []

    if profile["driver"]:
        args.extend(("--driver", profile["driver"]))

    if profile["container_runtime"]:
        args.extend(("--container-runtime", profile["container_runtime"]))

    if profile["extra_disks"]:
        args.extend(("--extra-disks", str(profile["extra_disks"])))

    if profile["disk_size"]:
        args.extend(("--disk-size", profile["disk_size"]))  # "4g"

    if profile["network"]:
        args.extend(("--network", profile["network"]))

    if profile["nodes"]:
        args.extend(("--nodes", str(profile["nodes"])))

    if profile["cni"]:
        args.extend(("--cni", profile["cni"]))

    if profile["cpus"]:
        args.extend(("--cpus", str(profile["cpus"])))

    if profile["memory"]:
        args.extend(("--memory", profile["memory"]))

    if profile["addons"]:
        args.extend(("--addons", ",".join(profile["addons"])))

    if profile["service_cluster_ip_range"]:
        args.extend(("--service-cluster-ip-range", profile["service_cluster_ip_range"]))

    for pair in EXTRA_CONFIG:
        args.extend(("--extra-config", pair))

    if profile["extra_config"]:
        for pair in profile["extra_config"]:
            args.extend(("--extra-config", pair))

    if profile["feature_gates"]:
        # Unlike --extra-config this requires one comma separated value.
        args.extend(("--feature-gates", ",".join(profile["feature_gates"])))

    if verbose:
        args.append("--alsologtostderr")

    args.append("--insecure-registry=host.minikube.internal:5000")

    # TODO: Use --interactive=false when the bug is fixed.
    # https://github.com/kubernetes/minikube/issues/19518

    _watch("start", *args, profile=profile["name"], timeout=timeout)

    logging.info(
        "[%s] Cluster started in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )


def configure(profile, existing=False):
    """
    Load configuration done in setup() before the minikube cluster was
    started.

    Must be called after the cluster is started, before running any addon.
    """
    if not existing:
        if profile["containerd"]:
            logging.info("[%s] Configuring containerd", profile["name"])
            containerd.configure(sys.modules[__name__], profile)
        _configure_sysctl(profile["name"])
        _configure_systemd_resolved(profile["name"])

    if existing:
        _wait_for_fresh_status(profile)


def stop(profile):
    start = time.monotonic()
    logging.info("[%s] Stopping cluster", profile["name"])
    _watch("stop", profile=profile["name"])
    logging.info(
        "[%s] Cluster stopped in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )


def delete(profile):
    start = time.monotonic()
    logging.info("[%s] Deleting cluster", profile["name"])
    _watch("delete", profile=profile["name"])
    logging.info(
        "[%s] Cluster deleted in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )


def load(profile, image):
    start = time.monotonic()
    logging.info("[%s] Loading image", profile["name"])
    _watch("image", "load", image, profile=profile["name"])
    logging.info(
        "[%s] Image loaded in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )


def suspend(profile):
    if profile["driver"] != "kvm2":
        logging.warning("[%s] suspend supported only for kvm2 driver", profile["name"])
        return
    logging.info("[%s] Suspending cluster", profile["name"])
    cmd = ["virsh", "-c", "qemu:///system", "suspend", profile["name"]]
    for line in commands.watch(*cmd):
        logging.debug("[%s] %s", profile["name"], line)


def resume(profile):
    if profile["driver"] != "kvm2":
        logging.warning("[%s] resume supported only for kvm2 driver", profile["name"])
        return
    logging.info("[%s] Resuming cluster", profile["name"])
    cmd = ["virsh", "-c", "qemu:///system", "resume", profile["name"]]
    for line in commands.watch(*cmd):
        logging.debug("[%s] %s", profile["name"], line)


def cp(name, src, dst):
    _watch("cp", src, dst, profile=name)


def ssh(name, command):
    _watch("ssh", command, profile=name)


# Private helpers


def _wait_for_fresh_status(profile):
    """
    When starting an existing cluster, kubectl can report stale status for a
    while, before it starts to report real status. Then it takes a while until
    all deployments become available.

    We wait 30 seconds to give Kubernetes chance to fail liveness and readiness
    checks and start reporting real cluster status.
    """
    logging.info("[%s] Waiting for fresh status", profile["name"])
    time.sleep(30)


def _profile(command, output=None):
    # Workaround for https://github.com/kubernetes/minikube/pull/16900
    # TODO: remove when issue is fixed.
    _create_profiles_dir()

    return _run("profile", command, output=output)


def _status(name, output=None):
    return _run("status", profile=name, output=output)


def _version():
    """
    Get minikube version string ("v1.33.1") and return a package.version.Version
    instance.
    """
    out = _run("version", output="json")
    info = json.loads(out)
    return Version(info["minikubeVersion"])


def _setup_sysctl(version):
    """
    Increase fs.inotify limits to avoid random timeouts when starting kubevirt
    VM.

    We use the same configuration as OpenShift worker node.
    See also https://www.suse.com/support/kb/doc/?id=000020048
    """
    if version >= Version("v1.33.1"):
        logging.debug("[minikube] Skipping sysctl configuration")
        return

    path = _sysctl_drenv_conf()
    data = """# Added by drenv setup
fs.inotify.max_user_instances = 8192
fs.inotify.max_user_watches = 65536
"""
    logging.debug("[minikube] Writing drenv sysctl configuration %s", path)
    _write_file(path, data)


def _configure_sysctl(name):
    if not os.path.exists(_sysctl_drenv_conf()):
        return
    logging.debug("[%s] Loading drenv sysctl configuration", name)
    ssh(name, "sudo sysctl -p /etc/sysctl.d/99-drenv.conf")


def _sysctl_drenv_conf():
    return _minikube_file("etc", "sysctl.d", "99-drenv.conf")


def _setup_systemd_resolved(version):
    """
    Disable DNSSEC in systemd-resolved configuration.

    This is workaround for minikube regression in 1.33.0:
    https://github.com/kubernetes/minikube/issues/18705

    TODO: Remove when issue is fixed in minikube.
    """
    if version >= Version("v1.33.1"):
        logging.debug("[minikube] Skipping systemd-resolved configuration")
        return

    path = _systemd_resolved_drenv_conf()
    data = """# Added by drenv setup
[Resolve]
DNSSEC=no
"""
    logging.debug("[minikube] Writing drenv systemd-resolved configuration %s", path)
    _write_file(path, data)


def _configure_systemd_resolved(name):
    if not os.path.exists(_systemd_resolved_drenv_conf()):
        return
    logging.debug("[%s] Loading drenv systemd-resolved configuration", name)
    ssh(name, "sudo systemctl restart systemd-resolved.service")


def _systemd_resolved_drenv_conf():
    return _minikube_file("etc", "systemd", "resolved.conf.d", "99-drenv.conf")


def _write_file(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        f.write(data)


def _cleanup_file(path):
    """
    Remove path and empty directories up to $MINIKUBE_HOME/.minikube/files/.
    """
    try:
        os.remove(path)
    except FileNotFoundError:
        pass
    else:
        logging.debug("[minikube] Removed file %s", path)

    stop = _minikube_file()
    while True:
        path = os.path.dirname(path)
        if path == stop:
            return
        try:
            os.rmdir(path)
        except FileNotFoundError:
            return
        except OSError as e:
            if e.errno != errno.ENOTEMPTY:
                raise
            return
        else:
            logging.debug("[minikube] Removed empty directory %s", path)


def _minikube_file(*names):
    """
    Create a path into $MINIKUBE_HOME/.minikube/files/...

    The files are injected into the VM when the VM is created.
    """
    base = os.environ.get("MINIKUBE_HOME", os.path.expanduser("~"))
    return os.path.join(base, ".minikube", "files", *names)


def _run(command, *args, profile=None, output=None):
    cmd = ["minikube", command]
    if profile:
        cmd.extend(("--profile", profile))
    if output:
        cmd.extend(("--output", output))
    cmd.extend(args)
    return commands.run(*cmd)


def _watch(command, *args, profile=None, timeout=None):
    cmd = ["minikube", command, "--profile", profile]
    cmd.extend(args)
    logging.debug("[%s] Running %s", profile, cmd)
    for line in commands.watch(*cmd, timeout=timeout):
        logging.debug("[%s] %s", profile, line)


def _create_profiles_dir():
    minikube_home = os.environ.get("MINIKUBE_HOME", os.environ.get("HOME"))
    profiles = os.path.join(minikube_home, ".minikube", "profiles")
    logging.debug("Creating '%s'", profiles)
    os.makedirs(profiles, exist_ok=True)
