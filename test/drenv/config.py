# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os

from . import yaml

_PATH = os.path.expanduser(os.path.join("~", ".config", "drenv", "config.yaml"))


REGISTRY_HOST = "registry_host"


def read():
    """
    Read the global drenv configuration from ~/.config/drenv/config.yaml.

    Returns the parsed yaml as a dict, or an empty dict if the file does
    not exist.
    """
    if not os.path.exists(_PATH):
        return {}
    with open(_PATH) as f:
        return yaml.safe_load(f)
