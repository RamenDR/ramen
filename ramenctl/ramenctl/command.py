# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import logging
import os

import yaml

from drenv import commands

RAMEN_NAMESPACE = "ramen-system"
SOURCE_DIR = "."

log = logging.getLogger("ramenctl")


def resource(name):
    """
    Locate resource in the package 'resources' directory.
    """
    pkg_dir = os.path.dirname(__file__)
    return os.path.join(pkg_dir, "resources", name)


def env_info(args):
    """
    Load ramen environment info from drenv environment file specified in
    command line arguments.
    """
    with open(args.filename) as f:
        env = yaml.safe_load(f)

    ramen = env["ramen"]

    if args.name_prefix:
        ramen["hub"] = args.name_prefix + info["hub"]
        ramen["clusters"] = [args.name_prefix + cluster for cluster in info["clusters"]]

    return ramen


def add_common_arguments(parser):
    """
    Argument needed by all commands.
    """
    parser.add_argument(
        "-v",
        "--verbose",
        action="store_true",
        help="Be more verbose",
    )
    parser.add_argument(
        "--name-prefix",
        help="Prefix profile names",
    )
    parser.add_argument(
        "filename",
        help="Environment filename",
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


def run(*args):
    return commands.run(*args)


def watch(*args, log=log.debug):
    for line in commands.watch(*args):
        log("%s", line)


info = log.info
debug = log.debug
