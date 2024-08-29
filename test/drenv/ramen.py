# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import logging

import drenv
from . import kubectl
from . import yaml


def env_info(filename, name_prefix=None):
    """
    Load ramen environment info from drenv environment file.
    """
    with open(filename) as f:
        env = yaml.safe_load(f)

    info = env["ramen"]

    if name_prefix:
        info["hub"] = name_prefix + info["hub"]
        info["clusters"] = [name_prefix + cluster for cluster in info["clusters"]]

    return info


def dump_e2e_config(env):
    """
    Dump configuration for ramen e2e framework.
    """
    base = drenv.config_dir(env["name"])
    logging.info("[%s] Dumping ramen e2e config to '%s'", env["name"], base)

    kubeconfigs_dir = os.path.join(base, "kubeconfigs")
    os.makedirs(kubeconfigs_dir, exist_ok=True)

    e2e_config = {"Clusters": {}}

    # Map e2e cluster names to actual cluster names.
    clusters = zip(
        [env["ramen"]["hub"], *env["ramen"]["clusters"]],
        ["hub", "c1", "c2"],
    )

    for cluster_name, e2e_name in clusters:
        if cluster_name is None:
            continue

        # Create standlone config file for this cluster.
        data = kubectl.config("view", "--flatten", "--minify", context=cluster_name)
        path = os.path.join(kubeconfigs_dir, cluster_name)
        with open(path, "w") as f:
            f.write(data)

        e2e_config["Clusters"][e2e_name] = {"kubeconfigpath": path}

    path = os.path.join(base, "config.yaml")
    with open(path, "w") as f:
        yaml.dump(e2e_config, f)
