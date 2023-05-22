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
    command.add_env_arguments(parser)


def run(args):
    command.info("Deleting samples channel")
    kubectl.delete(
        "--filename",
        command.resource("samples-channel.yaml"),
        "--ignore-not-found",
        context=args.hub_name,
        log=command.debug,
    )

    command.info("Deleting DRClusters and DRPolicy")
    kubectl.delete(
        "--kustomize",
        command.resource(args.env_type),
        "--ignore-not-found",
        context=args.hub_name,
        log=command.debug,
    )

    # We keep the ramen config map since we do not own it.

    command.info("Deleting s3 secret in ramen hub namespace")
    kubectl.delete(
        "--filename",
        command.resource("s3-secret.yaml"),
        "--ignore-not-found",
        context=args.hub_name,
        log=command.debug,
    )
