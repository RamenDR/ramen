# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import commands


def set_alias(cluster, url, access_key, secret_key):
    """
    Set mcli alias for cluster.
    """
    commands.run("mcli", "alias", "set", cluster, url, access_key, secret_key)


def remove_alias(cluster):
    """
    Remove mcli alias for cluster.
    """
    commands.run("mcli", "alias", "remove", cluster)


def mb(*targets, ignore_existing=False):
    """
    Create one of more buckets
    """
    cmd = ["mcli", "mb"]
    if ignore_existing:
        cmd.append("--ignore-existing")
    cmd.extend(targets)
    commands.run(*cmd)


def cp(*args):
    """
    Copy one or more soruces to target.
    """
    commands.run("mcli", "cp", *args)


def rm(*targets):
    """
    Remove one or more targets.
    """
    commands.run("mcli", "rm", *targets)


def cat(*targets):
    """
    Concatenate one or more targets.
    """
    return commands.run("mcli", "cat", *targets, decode=False)


def ls(*targets, recursive=False):
    """
    List objects and buckets.
    """
    cmd = ["mcli", "ls"]
    if recursive:
        cmd.append("--recursive")
    cmd.extend(targets)
    return commands.run(*cmd)
