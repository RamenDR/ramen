# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import drenv
from drenv import kubectl
from drenv import minio

from . import command


def register(commands):
    parser = commands.add_parser(
        "config",
        help="Configure ramen hub operator",
    )
    parser.set_defaults(func=run)
    command.add_common_arguments(parser)
    command.add_ramen_arguments(parser)


def run(args):
    env = command.env_info(args)

    s3_secrets = generate_ramen_s3_secrets(env["clusters"], args)

    if env["hub"]:
        hub_cm = generate_config_map("hub", env, args)

        wait_for_ramen_hub_operator(env["hub"], args)

        create_ramen_s3_secrets(env["hub"], s3_secrets)

        create_ramen_config_map(env["hub"], hub_cm)
        create_hub_dr_resources(env["hub"], env["clusters"], env["topology"])

        wait_for_secret_propagation(env["hub"], env["clusters"], args)
        wait_for_dr_clusters(env["hub"], env["clusters"], args)
        wait_for_dr_policy(env["hub"], args)
    else:
        dr_cluster_cm = generate_config_map("dr-cluster", env, args)

        for cluster in env["clusters"]:
            create_ramen_s3_secrets(cluster, s3_secrets)
            create_ramen_config_map(cluster, dr_cluster_cm)


def wait_for_ramen_hub_operator(hub, args):
    command.info("Waiting until ramen-hub-operator is rolled out")
    kubectl.rollout(
        "status",
        "deploy/ramen-hub-operator",
        f"--namespace={args.ramen_namespace}",
        "--timeout=180s",
        context=hub,
        log=command.debug,
    )


def generate_ramen_s3_secrets(clusters, args):
    template = drenv.template(command.resource("ramen-s3-secret.yaml"))
    return [
        template.substitute(namespace=args.ramen_namespace, cluster=cluster)
        for cluster in clusters
    ]


def create_ramen_s3_secrets(cluster, secrets):
    command.info("Creating ramen s3 secrets in cluster '%s'", cluster)
    for secret in secrets:
        kubectl.apply("--filename=-", input=secret, context=cluster, log=command.debug)


def generate_config_map(controller, env, args):
    clusters = env["clusters"]
    volsync = env["features"].get("volsync", True)
    template = drenv.template(command.resource("configmap.yaml"))
    return template.substitute(
        name=f"ramen-{controller}-operator-config",
        auto_deploy="true",
        cluster1=clusters[0],
        cluster2=clusters[1],
        minio_url_cluster1=minio.service_url(clusters[0]),
        minio_url_cluster2=minio.service_url(clusters[1]),
        volsync_disabled="false" if volsync else "true",
        namespace=args.ramen_namespace,
    )


def create_ramen_config_map(cluster, yaml):
    command.info("Updating ramen config map in cluster '%s'", cluster)
    kubectl.apply("--filename=-", input=yaml, context=cluster, log=command.debug)


def create_hub_dr_resources(hub, clusters, topology):
    for name in ["dr-clusters", "dr-policy"]:
        command.info("Creating %s for %s", name, topology)
        template = drenv.template(command.resource(f"{topology}/{name}.yaml"))
        yaml = template.substitute(cluster1=clusters[0], cluster2=clusters[1])
        kubectl.apply("--filename=-", input=yaml, context=hub, log=command.debug)


def wait_for_secret_propagation(hub, clusters, args):
    command.info("Waiting until s3 secrets are propagated to managed clusters")
    for cluster in clusters:
        policy = f"{args.ramen_namespace}.ramen-s3-secret-{cluster}"
        command.debug("Waiting until policy '%s' reports status", policy)
        drenv.wait_for(
            f"policy/{policy}",
            output="jsonpath={.status}",
            namespace=cluster,
            timeout=30,
            profile=hub,
            log=command.debug,
        )
        command.debug("Waiting until policy %s is compliant", policy)
        kubectl.wait(
            f"policy/{policy}",
            "--for=jsonpath={.status.compliant}=Compliant",
            "--timeout=30s",
            f"--namespace={cluster}",
            context=hub,
            log=command.debug,
        )


def wait_for_dr_clusters(hub, clusters, args):
    command.info("Waiting until DRClusters report phase")
    for name in clusters:
        drenv.wait_for(
            f"drcluster/{name}",
            output="jsonpath={.status.phase}",
            timeout=180,
            profile=hub,
            log=command.debug,
        )

    command.info("Waiting until DRClusters phase is available")
    kubectl.wait(
        "drcluster",
        "--all",
        "--for=jsonpath={.status.phase}=Available",
        context=hub,
        log=command.debug,
    )


def wait_for_dr_policy(hub, args):
    command.info("Waiting until DRPolicy is validated")
    kubectl.wait(
        "drpolicy/dr-policy",
        "--for=condition=Validated",
        context=hub,
        log=command.debug,
    )
