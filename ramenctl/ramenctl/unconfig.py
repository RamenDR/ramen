# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import drenv
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

    # Note: We keep the ramen config map since we do not own it.

    if env["hub"]:
        delete_hub_dr_resources(env["hub"], env["clusters"], env["topology"])
        delete_s3_secret([env["hub"]], args)
        delete_cloud_credentials(env["clusters"], args)
    else:
        delete_s3_secret(env["clusters"], args)
        delete_cloud_credentials(env["clusters"], args)


def delete_hub_dr_resources(hub, clusters, topology):
    # Deleting in reverse order.
    for name in ["dr-policy", "dr-clusters"]:
        command.info("Deleting %s for %s", name, topology)
        template = drenv.template(command.resource(f"{topology}/{name}.yaml"))
        yaml = template.substitute(cluster1=clusters[0], cluster2=clusters[1])
        kubectl.delete(
            "--filename=-",
            "--ignore-not-found",
            input=yaml,
            context=hub,
            log=command.debug,
        )


def delete_s3_secret(clusters, args):
    template = drenv.template(command.resource("ramen-s3-secret.yaml"))
    yaml = template.substitute(namespace=args.ramen_namespace)
    for cluster in clusters:
        command.info("Deleting s3 secret in cluster '%s'", cluster)
        kubectl.delete(
            "--filename=-",
            "--ignore-not-found",
            input=yaml,
            context=cluster,
            log=command.debug,
        )


def delete_cloud_credentials(clusters, args):
    template = drenv.template(command.resource("cloud-credentials-secret.yaml"))
    yaml = template.substitute(cloud="", namespace=args.ramen_namespace)
    for cluster in clusters:
        command.info("Deleting cloud credentials secret in cluster '%s'", cluster)
        kubectl.delete(
            "--filename=-",
            "--ignore-not-found",
            input=yaml,
            context=cluster,
            log=command.debug,
        )
