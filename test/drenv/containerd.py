# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import tempfile

import toml

from . import patch
from . import registry


def configure(provider, name, config):
    """
    Configure containerd and restart the service.

    provider: the VM provider module (e.g., minikube)
    name: the cluster/profile name
    config: containerd configuration to merge with existing config
    """
    remote_config = f"{name}:/etc/containerd/config.toml"

    with tempfile.TemporaryDirectory() as tmpdir:
        tmp = os.path.join(tmpdir, "config.toml")

        provider.cp(name, remote_config, tmp)
        with open(tmp) as f:
            old_config = toml.load(f)

        new_config = patch.merge(old_config, config)

        with open(tmp, "w") as f:
            toml.dump(new_config, f)
        provider.cp(name, tmp, remote_config)

    provider.ssh(name, "sudo systemctl restart containerd")


# See https://github.com/containerd/containerd/blob/main/docs/hosts.md
_HOSTS_TOML = """\
server = "{upstream}"

[host."http://{registry_host}:{port}"]
  capabilities = ["pull", "resolve"]

[host."{upstream}"]
  capabilities = ["pull", "resolve"]
"""


def create_registry_mirrors(path, registry_host):
    """
    Create a certs.d directory tree with hosts.toml for every registry
    in registry.REGISTRIES, pointing to the cache at registry_host.
    """
    for reg_name, reg in registry.REGISTRIES.items():
        reg_dir = os.path.join(path, reg_name)
        os.makedirs(reg_dir)
        hosts_toml = os.path.join(reg_dir, "hosts.toml")
        with open(hosts_toml, "w") as f:
            f.write(
                _HOSTS_TOML.format(
                    upstream=reg["upstream"],
                    registry_host=registry_host,
                    port=reg["port"],
                )
            )
