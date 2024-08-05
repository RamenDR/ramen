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

    command.info("Preparing resources")
    command.watch("make", "-C", args.source_dir, "resources")

    with tempfile.TemporaryDirectory(prefix="ramenctl-deploy-") as tmpdir:
        tar = os.path.join(tmpdir, "image.tar")
        command.info("Saving image '%s'", args.image)
        command.watch("podman", "save", args.image, "-o", tar)

        with concurrent.futures.ThreadPoolExecutor() as executor:
            futures = []

            if env["hub"]:
                f = executor.submit(
                    deploy, args, env["hub"], tar, "hub", platform="k8s"
                )
                futures.append(f)

            for cluster in env["clusters"]:
                f = executor.submit(deploy, args, cluster, tar, "dr-cluster")
                futures.append(f)

            for f in concurrent.futures.as_completed(futures):
                f.result()


def deploy(args, cluster, tar, deploy_type, platform="", timeout=120):
    command.info("Loading image in cluster '%s'", cluster)
    command.watch("minikube", "--profile", cluster, "image", "load", tar)

    command.info("Deploying ramen operator in cluster '%s'", cluster)
    overlay = os.path.join(args.source_dir, f"config/{deploy_type}/default", platform)
    yaml = kubectl.kustomize(overlay, load_restrictor="LoadRestrictionsNone")
    kubectl.apply("--filename=-", input=yaml, context=cluster, log=command.debug)

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
