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
    env = command.env_info(args)

    for cluster in env["clusters"]:
        command.info("Undeploying ramen operator in cluster '%s'", cluster)
        command.watch("kubectl", "config", "use-context", cluster)
        command.watch("make", "-C", args.source_dir, "undeploy-dr-cluster")

    if env["hub"]:
        command.info("Undeploying ramen operator in cluster '%s'", env["hub"])
        command.watch("kubectl", "config", "use-context", env["hub"])
        command.watch("make", "-C", args.source_dir, "undeploy-hub")
