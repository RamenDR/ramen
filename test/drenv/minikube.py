# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import logging

from . import commands


def profile(command, output=None):
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
    if alsologtostderr:
        args.append("--alsologtostderr")

    _watch("start", *args, profile=profile)


def stop(profile):
    _watch("stop", profile=profile)


def delete(profile):
    _watch("delete", profile=profile)


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
    for line in commands.watch(*cmd):
        logging.debug("[%s] %s", profile, line)
