# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import tempfile

import toml

from . import minikube
from . import patch


def configure(profile):
    config = f"{profile['name']}:/etc/containerd/config.toml"

    with tempfile.TemporaryDirectory() as tmpdir:
        tmp = os.path.join(tmpdir, "config.toml")

        minikube.cp(profile["name"], config, tmp)
        with open(tmp) as f:
            old_config = toml.load(f)

        new_config = patch.merge(old_config, profile["containerd"])

        with open(tmp, "w") as f:
            toml.dump(new_config, f)
        minikube.cp(profile["name"], tmp, config)

    minikube.ssh(profile["name"], "sudo systemctl restart containerd")
