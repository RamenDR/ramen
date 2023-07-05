# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import yaml


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
