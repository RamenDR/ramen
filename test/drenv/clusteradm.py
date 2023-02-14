# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from . import commands


def init(wait=False, context=None):
    """
    Initialize a Kubernetes cluster into an OCM hub cluster.
    """
    cmd = ["clusteradm", "init"]
    if wait:
        cmd.append("--wait")
    if context:
        cmd.extend(("--context", context))
    _watch(*cmd)


def get(what, context=None):
    """
    Get information from the cluster.
    """
    cmd = ["clusteradm", "get", what]
    if context:
        cmd.extend(("--context", context))
    return commands.run(*cmd)


def install(what, names, bundle_version=None, context=None):
    """
    Install a feature.
    """
    cmd = ["clusteradm", "install", what, "--names", ",".join(names)]
    if bundle_version:
        cmd.extend(("--bundle-version", bundle_version))
    if context:
        cmd.extend(("--context", context))
    _watch(*cmd)


def join(hub_token, hub_apiserver, cluster_name, wait=False, context=None):
    """
    Join a cluster to the hub.
    """
    cmd = [
        "clusteradm",
        "join",
        "--hub-token",
        hub_token,
        "--hub-apiserver",
        hub_apiserver,
        "--cluster-name",
        cluster_name,
    ]
    if wait:
        cmd.append("--wait")
    if context:
        cmd.extend(("--context", context))
    _watch(*cmd)


def accept(clusters, wait=False, context=None):
    """
    Accept clusters to the hub.
    """
    cmd = ["clusteradm", "accept", "--clusters", ",".join(clusters)]
    if wait:
        cmd.append("--wait")
    if context:
        cmd.extend(("--context", context))
    _watch(*cmd)


def addon(action, names, clusters, context=None):
    """
    Enable or disable addons on clusters.
    """
    cmd = [
        "clusteradm",
        "addon",
        action,
        "--names",
        ",".join(names),
        "--clusters",
        ",".join(clusters),
    ]
    if context:
        cmd.extend(("--context", context))
    _watch(*cmd)


def _watch(*cmd):
    for line in commands.watch(*cmd):
        print(line)
