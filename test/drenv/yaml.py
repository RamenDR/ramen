# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0
"""
This module wraps pyyaml for preserving multiline strings when dumping, and
enforcing our defaults.

Do not use the yaml module directly in the drenv package.
"""

import yaml


def safe_load(stream):
    return yaml.safe_load(stream)


def safe_load_all(stream):
    return yaml.safe_load_all(stream)


def dump(data, stream=None):
    return yaml.dump(data, stream=stream, sort_keys=False)


def _str_presenter(dumper, data):
    """
    Preserve multiline strings when dumping yaml.
    https://github.com/yaml/pyyaml/issues/240
    """
    if "\n" in data:
        # Remove trailing spaces messing out the output.
        block = "\n".join([line.rstrip() for line in data.splitlines()])
        if data.endswith("\n"):
            block += "\n"
        return dumper.represent_scalar("tag:yaml.org,2002:str", block, style="|")
    return dumper.represent_scalar("tag:yaml.org,2002:str", data)


yaml.add_representer(str, _str_presenter)
yaml.representer.SafeRepresenter.add_representer(str, _str_presenter)
