# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from . import commands


def init(feature_gates=None, wait=False, context=None, log=print):
    """
    Initialize a Kubernetes cluster into an OCM hub cluster.
    """
    cmd = ["clusteradm", "init"]
    if feature_gates:
        cmd.extend(("--feature-gates", ",".join(feature_gates)))
    if wait:
        cmd.append("--wait")
    if context:
        cmd.extend(("--context", context))
    _watch(*cmd, log=log)


def get(what, output=None, context=None):
    """
    Get information from the cluster.
    """
    cmd = ["clusteradm", "get", what]
    if output:
        cmd.extend(("--output", output))
    if context:
        cmd.extend(("--context", context))
    return commands.run(*cmd)


def install(what, names, bundle_version=None, context=None, log=print):
    """
    Install a feature.
    """
    cmd = ["clusteradm", "install", what, "--names", ",".join(names)]
    if bundle_version:
        cmd.extend(("--bundle-version", bundle_version))
    if context:
        cmd.extend(("--context", context))
    _watch(*cmd, log=log)


def join(hub_token, hub_apiserver, cluster_name, wait=False, context=None, log=print):
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
    _watch(*cmd, log=log)


def addon(action, names, clusters, context=None, log=print):
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
    _watch(*cmd, log=log)


def _watch(*cmd, log=print):
    for line in commands.watch(*cmd):
        log(line)
