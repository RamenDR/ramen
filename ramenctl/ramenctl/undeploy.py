# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from . import command


def register(commands):
    parser = commands.add_parser(
        "undeploy",
        help="Undeploy ramen on the hub and managed clusters",
    )
    parser.set_defaults(func=run)
    command.add_common_arguments(parser)
    command.add_source_arguments(parser)


def run(args):
    for cluster in args.clusters_names:
        command.info("Undeploying ramen operator in cluster '%s'", cluster)
        command.watch("kubectl", "config", "use-context", cluster)
        command.watch("make", "-C", args.source_dir, "undeploy-dr-cluster")

    command.info("Undeploying ramen operator in cluster '%s'", args.hub_name)
    command.watch("kubectl", "config", "use-context", args.hub_name)
    command.watch("make", "-C", args.source_dir, "undeploy-hub")
