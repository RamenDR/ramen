# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import errno
import json
import logging
import os

from packaging.version import Version

from . import commands

EXTRA_CONFIG = [
    # When enabled, tells the Kubelet to pull images one at a time. This slows
    # down concurrent image pulls and cause timeouts when using slow network.
    # Defaults to true becasue it is not safe with docker daemon with version
    # < 1.9 or an Aufs storage backend.
    # https://github.com/kubernetes/kubernetes/issues/10959
    # Speeds up regional-dr start by 20%.
    "kubelet.serialize-image-pulls=false"
]


def profile(command, output=None):
    # Workaround for https://github.com/kubernetes/minikube/pull/16900
    # TODO: remove when issue is fixed.
    _create_profiles_dir()

    return _run("profile", command, output=output)


def status(profile, output=None):
    return _run("status", profile=profile, output=output)


def start(
    profile,
    driver=None,
    container_runtime=None,
    extra_disks=None,
    disk_size=None,
    network=None,
    nodes=None,
    cni=None,
    cpus=None,
    memory=None,
    addons=(),
    service_cluster_ip_range=None,
    extra_config=None,
    feature_gates=None,
    alsologtostderr=False,
):
    args = []

    if driver:
        args.extend(("--driver", driver))
    if container_runtime:
        args.extend(("--container-runtime", container_runtime))
    if extra_disks:
        args.extend(("--extra-disks", str(extra_disks)))
    if disk_size:
        args.extend(("--disk-size", disk_size))  # "4g"
    if network:
        args.extend(("--network", network))
    if nodes:
        args.extend(("--nodes", str(nodes)))
    if cni:
        args.extend(("--cni", cni))
    if cpus:
        args.extend(("--cpus", str(cpus)))
    if memory:
        args.extend(("--memory", memory))
    if addons:
        args.extend(("--addons", ",".join(addons)))
    if service_cluster_ip_range:
        args.extend(("--service-cluster-ip-range", service_cluster_ip_range))

    for pair in EXTRA_CONFIG:
        args.extend(("--extra-config", pair))

    if extra_config:
        for pair in extra_config:
            args.extend(("--extra-config", pair))

    if feature_gates:
        # Unlike --extra-config this requires one comma separated value.
        args.extend(("--feature-gates", ",".join(feature_gates)))

    if alsologtostderr:
        args.append("--alsologtostderr")

    _watch("start", *args, profile=profile)


def stop(profile):
    _watch("stop", profile=profile)


def delete(profile):
    _watch("delete", profile=profile)


def cp(profile, src, dst):
    _watch("cp", src, dst, profile=profile)


def ssh(profile, command):
    _watch("ssh", command, profile=profile)


def setup_files():
    """
    Set up minikube to work with drenv. Must be called before starting the
    first cluster.

    To load the configuration you must call load_files() after a cluster is
    created.
    """
    version = _version()
    logging.debug("[minikube] Using minikube version %s", version)
    _setup_sysctl(version)
    _setup_systemd_resolved(version)


def load_files(profile):
    """
    Load configuration done in setup_files() before the minikube cluster was
    started.

    Must be called after the cluster is started, before running any addon. Not
    need when starting a stopped cluster.
    """
    _load_sysctl(profile)
    _load_systemd_resolved(profile)


def cleanup_files():
    """
    Cleanup files added by setup_files().
    """
    _cleanup_file(_systemd_resolved_drenv_conf())
    _cleanup_file(_sysctl_drenv_conf())


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


def _load_sysctl(profile):
    if not os.path.exists(_sysctl_drenv_conf()):
        return
    logging.debug("[%s] Loading drenv sysctl configuration", profile)
    ssh(profile, "sudo sysctl -p /etc/sysctl.d/99-drenv.conf")


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


def _load_systemd_resolved(profile):
    if not os.path.exists(_systemd_resolved_drenv_conf()):
        return
    logging.debug("[%s] Loading drenv systemd-resolved configuration", profile)
    ssh(profile, "sudo systemctl restart systemd-resolved.service")


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


def _watch(command, *args, profile=None):
    cmd = ["minikube", command, "--profile", profile]
    cmd.extend(args)
    logging.debug("[%s] Running %s", profile, cmd)
    for line in commands.watch(*cmd):
        logging.debug("[%s] %s", profile, line)


def _create_profiles_dir():
    minikube_home = os.environ.get("MINIKUBE_HOME", os.environ.get("HOME"))
    profiles = os.path.join(minikube_home, ".minikube", "profiles")
    logging.debug("Creating '%s'", profiles)
    os.makedirs(profiles, exist_ok=True)
