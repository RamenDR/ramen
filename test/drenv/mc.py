# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import commands


def set_alias(cluster, url, access_key, secret_key):
    """
    Set mc alias for cluster.
    """
    commands.run("mc", "alias", "set", cluster, url, access_key, secret_key)


def remove_alias(cluster):
    """
    Remove mc alias for cluster.
    """
    commands.run("mc", "alias", "remove", cluster)


def mb(*targets, ignore_existing=False):
    """
    Create one of more buckets
    """
    cmd = ["mc", "mb"]
    if ignore_existing:
        cmd.append("--ignore-existing")
    cmd.extend(targets)
    commands.run(*cmd)
