# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import logging
import os

from drenv import commands

HUB_NAME = "hub"
CLUSTERS_NAMES = ["dr1", "dr2"]
RAMEN_NAMESPACE = "ramen-system"
ENV_TYPES = ["regional-dr"]
SOURCE_DIR = "."

log = logging.getLogger("ramenctl")


def resource(name):
    """
    Locate resource in the package 'resources' directory.
    """
    pkg_dir = os.path.dirname(__file__)
    return os.path.join(pkg_dir, "resources", name)


def add_common_arguments(parser):
    """
    Argument needed by all commands.
    """
    parser.add_argument(
        "--hub-name",
        default=HUB_NAME,
        help=f"Hub cluster name (default '{HUB_NAME}')",
    )
    parser.add_argument(
        "--clusters-names",
        default=CLUSTERS_NAMES,
        type=lambda s: s.split(","),
        help=f"Managed clusters names (default '{','.join(CLUSTERS_NAMES)}')",
    )
    parser.add_argument(
        "-v",
        "--verbose",
        action="store_true",
        help="Be more verbose",
    )


def add_ramen_arguments(parser):
    """
    Arguemnts for commands accessing ramen deployment.
    """
    parser.add_argument(
        "--ramen-namespace",
        default=RAMEN_NAMESPACE,
        help=f"Ramen namespace (default '{RAMEN_NAMESPACE}')",
    )


def add_source_arguments(parser):
    """
    Arguments for commands using ramen Makefile or other files from the source
    tree.
    """
    parser.add_argument(
        "--source-dir",
        default=SOURCE_DIR,
        help=f"The ramen source directory (default '{SOURCE_DIR}')",
    )


def add_env_arguments(parser):
    """
    Arguemnts related to the test environment.
    """
    parser.add_argument(
        "env_type",
        choices=ENV_TYPES,
        help="Environment type",
    )


def run(*args):
    return commands.run(*args)


def watch(*args, log=log.debug):
    for line in commands.watch(*args):
        log("%s", line)


info = log.info
debug = log.debug
