# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import concurrent.futures
import os
import tempfile

from drenv import kubectl
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
    command.add_ramen_arguments(parser)
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

        all_clusters = env["clusters"][:]
        if env["hub"]:
            all_clusters.append(env["hub"])

        with concurrent.futures.ThreadPoolExecutor() as executor:
            list(executor.map(load_image, all_clusters))

    if env["hub"]:
        command.info("Deploying ramen operator in cluster '%s'", env["hub"])
        command.watch("kubectl", "config", "use-context", env["hub"])
        command.watch("make", "-C", args.source_dir, "deploy-hub")

    for cluster in env["clusters"]:
        command.info("Deploying ramen operator in cluster '%s'", cluster)
        command.watch("kubectl", "config", "use-context", cluster)
        command.watch("make", "-C", args.source_dir, "deploy-dr-cluster")

    if env["hub"]:
        wait_for_ramen_deployment(args, env["hub"], "hub")

    for cluster in env["clusters"]:
        wait_for_ramen_deployment(args, cluster, "dr-cluster")


def wait_for_ramen_deployment(args, cluster, deploy_type, timeout=120):
    deploy = f"ramen-{deploy_type}-operator"
    command.info("Waiting until '%s' is rolled out in cluster '%s'", deploy, cluster)
    kubectl.rollout(
        "status",
        f"deploy/{deploy}",
        f"--namespace={args.ramen_namespace}",
        f"--timeout={timeout}s",
        context=cluster,
        log=command.debug,
    )
