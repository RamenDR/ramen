# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from . import commands


def ssh(vm, command, username=None, namespace=None, known_hosts=None, context=None):
    """
    Run ssh command via virtctl.
    """
    cmd = ["virtctl", "ssh"]
    if context:
        cmd.extend(("--context", context))
    if username:
        cmd.extend(("--username", username))
    if known_hosts is not None:
        cmd.extend(("--known-hosts", known_hosts))
    if namespace:
        cmd.extend(("--namespace", namespace))
    cmd.extend((vm, "--command", command))
    return commands.run(*cmd)
