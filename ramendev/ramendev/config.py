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

        create_ramen_s3_secrets(env["hub"], s3_secrets)

        create_ramen_config_map(env["hub"], hub_cm)
        create_hub_dr_resources(env["hub"], env["clusters"], env["topology"])

        wait_for_secret_propagation(env["hub"], env["clusters"], args)
        wait_for_dr_clusters(env["hub"], env["clusters"], args)
        wait_for_dr_policy(env["hub"], env.get("topology"))

        # ramen-ops namespace is created by the drpolicy controller. It should
        # exist when the dr policy is validated.
        create_ramen_ops_binding(env["hub"])
    else:
        dr_cluster_cm = generate_config_map("dr-cluster", env, args)

        for cluster in env["clusters"]:
            create_ramen_s3_secrets(cluster, s3_secrets)
            create_ramen_config_map(cluster, dr_cluster_cm)


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


def create_ramen_ops_binding(cluster):
    command.info("Creating ramen-ops managedclustersetbinding in cluster '%s'", cluster)
    resource = command.resource("ramen-ops-binding.yaml")
    kubectl.apply(f"--filename={resource}", context=cluster, log=command.debug)


def create_hub_dr_resources(hub, clusters, topology):
    c1, c2 = clusters[0], clusters[1]
    command.info("Creating dr-clusters for %s", topology)
    template = drenv.template(command.resource(f"{topology}/dr-clusters.yaml"))
    yaml = template.substitute(cluster1=c1, cluster2=c2)
    kubectl.apply("--filename=-", input=yaml, context=hub, log=command.debug)

    policy_path = command.resource(f"{topology}/dr-policy.yaml")
    for spec in command.dr_policy_template(topology):
        command.info("Creating %s for %s", spec["policy_name"], topology)
        template = drenv.template(policy_path)
        yaml = template.substitute(cluster1=c1, cluster2=c2, **spec)
        kubectl.apply("--filename=-", input=yaml, context=hub, log=command.debug)


def wait_for_secret_propagation(hub, clusters, args):
    command.info("Waiting until s3 secrets are propagated to managed clusters")
    for cluster in clusters:
        policy = f"{args.ramen_namespace}.ramen-s3-secret-{cluster}"
        command.debug("Waiting until policy '%s/%s' exists", cluster, policy)
        kubectl.wait(
            f"policy/{policy}",
            "--for=create",
            f"--namespace={cluster}",
            timeout=60,
            context=hub,
            log=command.debug,
        )
        command.debug("Waiting until policy '%s/%s' is compliant", cluster, policy)
        kubectl.wait(
            f"policy/{policy}",
            "--for=jsonpath={.status.compliant}=Compliant",
            f"--namespace={cluster}",
            timeout=60,
            context=hub,
            log=command.debug,
        )


def wait_for_dr_clusters(hub, clusters, args):
    command.info("Waiting until DRClusters exist")
    for name in clusters:
        kubectl.wait(
            f"drcluster/{name}",
            "--for=create",
            timeout=180,
            context=hub,
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


def wait_for_dr_policy(hub, topology):
    for spec in command.dr_policy_template(topology):
        resource = f"drpolicy/{spec['policy_name']}"
        command.info("Waiting until %s is validated", resource)
        kubectl.wait(
            resource,
            "--for=condition=Validated",
            context=hub,
            log=command.debug,
        )
