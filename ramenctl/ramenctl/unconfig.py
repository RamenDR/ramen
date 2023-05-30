# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import kubectl

from . import command


def register(commands):
    parser = commands.add_parser(
        "unconfig",
        help="Unconfigure ramen hub operator",
    )
    parser.set_defaults(func=run)
    command.add_common_arguments(parser)
    command.add_ramen_arguments(parser)


def run(args):
    env = command.env_info(args)

    command.info("Deleting DRClusters and DRPolicy")
    kubectl.delete(
        "--kustomize",
        command.resource(env["topology"]),
        "--ignore-not-found",
        context=env["hub"],
        log=command.debug,
    )

    # We keep the ramen config map since we do not own it.

    command.info("Deleting s3 secret in ramen hub namespace")
    kubectl.delete(
        "--filename",
        command.resource("ramen-s3-secret.yaml"),
        "--ignore-not-found",
        context=env["hub"],
        log=command.debug,
    )
