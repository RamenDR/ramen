# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import concurrent.futures
import os
import tempfile

from . import command

IMAGE = "quay.io/ramendr/ramen-operator:latest"


def register(commands):
    parser = commands.add_parser(
        "deploy",
        help="Deploy ramen on the hub and managed clusters",
    )
    parser.set_defaults(func=run)
    command.add_common_arguments(parser)
    command.add_source_arguments(parser)
    parser.add_argument(
        "--image",
        default=IMAGE,
        help=f"The container image to deploy (default '{IMAGE}')",
    )


def run(args):
    env = command.env_info(args)

    with tempfile.TemporaryDirectory(prefix="ramenctl-deploy-") as tmpdir:
        tar = os.path.join(tmpdir, "image.tar")
        command.info("Saving image '%s'", args.image)
        command.watch("podman", "save", args.image, "-o", tar)

        def load_image(cluster):
            command.info("Loading image in cluster '%s'", cluster)
            command.watch("minikube", "--profile", cluster, "image", "load", tar)

        clusters = [env["hub"]] + env["clusters"]
        with concurrent.futures.ThreadPoolExecutor() as executor:
            list(executor.map(load_image, clusters))

    command.info("Deploying ramen operator in cluster '%s'", env["hub"])
    command.watch("kubectl", "config", "use-context", env["hub"])
    command.watch("make", "-C", args.source_dir, "deploy-hub")

    for cluster in env["clusters"]:
        command.info("Deploying ramen operator in cluster '%s'", cluster)
        command.watch("kubectl", "config", "use-context", cluster)
        command.watch("make", "-C", args.source_dir, "deploy-dr-cluster")
