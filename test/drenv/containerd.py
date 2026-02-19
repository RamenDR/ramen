# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import tempfile

import toml

from . import patch


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
