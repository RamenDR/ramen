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

    s3_secret = generate_ramen_s3_secret(args)
    cloud_secret = generate_cloud_credentials_secret(env["clusters"][0], args)
    hub_cm = generate_config_map("hub", env["clusters"], args)

    wait_for_ramen_hub_operator(env["hub"], args)

    create_ramen_s3_secret(env["hub"], s3_secret)
    for cluster in env["clusters"]:
        create_cloud_credentials_secret(cluster, cloud_secret)
    create_ramen_config_map(env["hub"], hub_cm)
    create_hub_dr_resources(env["hub"], env["clusters"], env["topology"])

    wait_for_dr_clusters(env["hub"], env["clusters"], args)
    wait_for_dr_policy(env["hub"], args)


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


def generate_ramen_s3_secret(args):
    template = drenv.template(command.resource("ramen-s3-secret.yaml"))
    return template.substitute(namespace=args.ramen_namespace)


def create_ramen_s3_secret(cluster, yaml):
    command.info("Creating ramen s3 secret in cluster '%s'", cluster)
    kubectl.apply("--filename=-", input=yaml, context=cluster, log=command.debug)


def generate_cloud_credentials_secret(cluster, args):
    command.debug("Getting velero cloud credentials from cluster '%s'", cluster)
    cloud = kubectl.get(
        "secret/cloud-credentials",
        "--namespace=velero",
        "--output=jsonpath={.data.cloud}",
        context=cluster,
    )
    template = drenv.template(command.resource("cloud-credentials-secret.yaml"))
    return template.substitute(cloud=cloud, namespace=args.ramen_namespace)


def create_cloud_credentials_secret(cluster, yaml):
    command.info("Creating cloud credentials secret in cluster '%s'", cluster)
    kubectl.apply("--filename=-", input=yaml, context=cluster, log=command.debug)


def generate_config_map(controller, clusters, args):
    template = drenv.template(command.resource("configmap.yaml"))
    return template.substitute(
        name=f"ramen-{controller}-operator-config",
        auto_deploy="true",
        cluster1=clusters[0],
        cluster2=clusters[1],
        minio_url_cluster1=minio.service_url(clusters[0]),
        minio_url_cluster2=minio.service_url(clusters[1]),
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


def wait_for_dr_clusters(hub, clusters, args):
    command.info("Waiting until DRClusters report phase")
    for name in clusters:
        drenv.wait_for(
            f"drcluster/{name}",
            output="jsonpath={.status.phase}",
            namespace=args.ramen_namespace,
            timeout=180,
            profile=hub,
            log=command.debug,
        )

    command.info("Waiting until DRClusters phase is available")
    kubectl.wait(
        "drcluster",
        "--all",
        "--for=jsonpath={.status.phase}=Available",
        f"--namespace={args.ramen_namespace}",
        context=hub,
        log=command.debug,
    )


def wait_for_dr_policy(hub, args):
    command.info("Waiting until DRPolicy is validated")
    kubectl.wait(
        "drpolicy/dr-policy",
        "--for=condition=Validated",
        f"--namespace={args.ramen_namespace}",
        context=hub,
        log=command.debug,
    )
