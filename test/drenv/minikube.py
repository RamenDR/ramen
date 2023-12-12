# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import logging
import os

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
