# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import concurrent.futures
import os
import subprocess
import tempfile
from datetime import datetime, timezone

from drenv import kubectl
from drenv import patch
from drenv import yaml

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

    load_image(args)

    with concurrent.futures.ThreadPoolExecutor() as executor:
        futures = []

        if env["hub"]:
            f = executor.submit(deploy, args, env["hub"], "hub", distro="k8s")
            futures.append(f)

        for cluster in env["clusters"]:
            f = executor.submit(deploy, args, cluster, "dr-cluster")
            futures.append(f)

        for f in concurrent.futures.as_completed(futures):
            f.result()


def load_image(args):
    command.info("Loading image '%s'", args.image)
    with tempfile.TemporaryDirectory(prefix="ramendev-deploy-") as tmpdir:
        tar = os.path.join(tmpdir, "image.tar")
        command.watch("podman", "save", args.image, "-o", tar)
        cmd = ["drenv", "load", f"--image={tar}"]
        if args.name_prefix:
            cmd.append(f"--name-prefix={args.name_prefix}")
        cmd.append(os.path.abspath(args.filename))
        work_dir = os.path.join(args.source_dir, "test") if args.source_dir else None
        command.watch(*cmd, stderr=subprocess.STDOUT, cwd=work_dir)


def deploy(args, cluster, deploy_type, distro="", timeout=120):
    deploy = f"ramen-{deploy_type}-operator"

    command.info("Deploying ramen operator in cluster '%s'", cluster)
    overlay = os.path.join(args.source_dir, f"config/{deploy_type}/default", distro)
    manifests = kubectl.kustomize(overlay, load_restrictor="LoadRestrictionsNone")
    kubectl.apply(
        "--filename=-",
        input=annotate_deployment(manifests, deploy),
        context=cluster,
        log=command.debug,
    )

    command.info("Waiting until '%s' is rolled out in cluster '%s'", deploy, cluster)
    kubectl.rollout(
        "status",
        f"deploy/{deploy}",
        f"--namespace={args.ramen_namespace}",
        timeout=timeout,
        context=cluster,
        log=command.debug,
    )


def annotate_deployment(manifests, name):
    """
    Annotate the Deployment pod template so apply rolls out new pods.

    The operator image tag is always :latest and imagePullPolicy is
    IfNotPresent. Applying the same manifests does not change spec.template,
    so Kubernetes keeps the running pods and they never pick up the image
    loaded by drenv.
    """
    timestamp = datetime.now(timezone.utc).isoformat()
    deployment_patch = {
        "spec": {
            "template": {
                "metadata": {
                    "annotations": {
                        "ramendr.openshift.io/deployed-at": timestamp,
                    }
                }
            }
        }
    }
    docs = []
    for doc in yaml.safe_load_all(manifests):
        if doc is None:
            continue
        if doc["kind"] == "Deployment" and doc["metadata"]["name"] == name:
            doc = patch.merge(doc, deployment_patch)
        docs.append(doc)
    return "".join("---\n" + yaml.safe_dump(doc) for doc in docs)
