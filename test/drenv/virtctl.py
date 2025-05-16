# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from . import commands


def ssh(
    vm,
    command,
    username=None,
    namespace=None,
    known_hosts=None,
    identity_file=None,
    local_ssh=True,
    local_ssh_opts=(),
    context=None,
):
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
    if identity_file:
        cmd.extend(("--identity-file", identity_file))
    if local_ssh:
        cmd.append("--local-ssh")
        for opt in local_ssh_opts:
            cmd.extend(("--local-ssh-opts", opt))
    cmd.extend((vm, "--command", command))
    return commands.run(*cmd)
