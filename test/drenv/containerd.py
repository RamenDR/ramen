# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import tempfile

import toml

from . import patch


def configure(provider, profile):
    config = f"{profile['name']}:/etc/containerd/config.toml"

    with tempfile.TemporaryDirectory() as tmpdir:
        tmp = os.path.join(tmpdir, "config.toml")

        provider.cp(profile["name"], config, tmp)
        with open(tmp) as f:
            old_config = toml.load(f)

        new_config = patch.merge(old_config, profile["containerd"])

        with open(tmp, "w") as f:
            toml.dump(new_config, f)
        provider.cp(profile["name"], tmp, config)

    provider.ssh(profile["name"], "sudo systemctl restart containerd")
