# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import argparse
import json
import logging
import os
import sys

import yaml

import drenv
from drenv import kubectl

from . import ramen


workdir = None
env = None
config = None
log = None
parser = None
log_format = "%(asctime)s %(levelname)-7s [%(name)s] %(message)s"


def start(name, file, config_file="config.yaml"):
    global workdir, config, parser, log

    # Setting up logging and sys.excepthook must be first so any failure will
    # be reported using the logger.
    log = logging.getLogger(name)
    sys.excepthook = _excepthook

    # We start with info level. After parsing the command line we may change
    # the level to debug.
    logging.basicConfig(level=logging.INFO, format=log_format)

    # Working directory for runing the test.
    workdir = os.path.abspath(os.path.dirname(file))

    config_path = os.path.join(workdir, config_file)
    with open(config_path) as f:
        config = yaml.safe_load(f)

    parser = argparse.ArgumentParser(name)
    parser.add_argument(
        "-v",
        "--verbose",
        action="store_true",
        help="Be more verbose.",
    )
    parser.add_argument(
        "--name-prefix",
        help="Prefix profile names",
    )
    parser.add_argument(
        "filename",
        help="Environment filename",
    )


def add_argument(*args, **kw):
    parser.add_argument(*args, **kw)


def parse_args():
    global env

    args = parser.parse_args()
    if args.verbose:
        log.setLevel(logging.DEBUG)
    debug("Parsed arguments: %s", args)

    env = ramen.env_info(args.filename, name_prefix=args.name_prefix)
    debug("Using environment: %s", env)

    debug("Entering directory '%s'", workdir)
    os.chdir(workdir)

    return args


def info(fmt, *args):
    log.info(fmt, *args)


def debug(fmt, *args):
    log.debug(fmt, *args)


def _excepthook(t, v, tb):
    log.exception("test failed", exc_info=(t, v, tb))


def deploy():
    """
    Deploy application on cluster.
    """
    info("Deploying channel")
    kubectl.apply(
        f"--kustomize={config['repo']}/channel?ref={config['branch']}",
        context=env["hub"],
        log=debug,
    )
    info("Deploying subscription based application")
    kubectl.apply(
        f"--kustomize={config['repo']}/subscription?ref={config['branch']}",
        context=env["hub"],
        log=debug,
    )


def undeploy():
    """
    Undeploy an application.
    """
    info("Undeploying channel")
    kubectl.delete(
        f"--kustomize={config['repo']}/channel?ref={config['branch']}",
        "--ignore-not-found",
        context=env["hub"],
        log=debug,
    )
    info("Undeploying subscription based application")
    kubectl.delete(
        f"--kustomize={config['repo']}/subscription?ref={config['branch']}",
        "--ignore-not-found",
        context=env["hub"],
        log=debug,
    )


def enable_dr():
    """
    Enable DR for deployed application.
    """
    cluster = lookup_cluster()

    # TODO: support placement
    info("Setting placementrule scheduler to ramen")
    _patch_placementrule({"spec": {"schedulerName": "ramen"}})

    drpc = f"""
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: busybox-drpc
  namespace: {config['namespace']}
  labels:
    app: {config['name']}
spec:
  preferredCluster: {cluster}
  drPolicyRef:
    name: dr-policy
  placementRef:
    kind: PlacementRule
    name: busybox-placement
  pvcSelector:
    matchLabels:
      appname: busybox
"""
    kubectl.apply("--filename=-", input=drpc, context=env["hub"], log=debug)


def disable_dr():
    """
    Disable DR for deployed application.
    """
    drpc = _lookup_app_resource("drpc")
    if not drpc:
        debug("drpc already removed")
        return

    info("Deleting '%s'", drpc)
    kubectl.delete(
        drpc,
        "--ignore-not-found",
        f"--namespace={config['namespace']}",
        context=env["hub"],
        log=debug,
    )

    # TODO: support placement
    info("Clearing placementrule scheduler")
    _patch_placementrule({"spec": {"schedulerName": None}})


def _patch_placementrule(patch):
    placementrule = _lookup_app_resource("placementrule")
    if not placementrule:
        raise RuntimeError(
            f"Cannot find placementrule for application {config['name']}"
        )

    kubectl.patch(
        placementrule,
        "--patch",
        json.dumps(patch),
        "--type=merge",
        f"--namespace={config['namespace']}",
        context=env["hub"],
        log=debug,
    )


def target_cluster():
    """
    Return the target cluster for failover or relocate.
    """
    cluster = lookup_cluster()
    if cluster == env["clusters"][0]:
        return env["clusters"][1]
    else:
        return env["clusters"][0]


def lookup_cluster():
    """
    Return the current cluster the application is placed on.
    """
    cluster = _get_cluster_from_placement_decisions()
    if not cluster:
        cluster = _get_cluster_from_placementrule()
        if not cluster:
            raise RuntimeError(f"Cannot find cluster for application {config['name']}")

    return cluster


def _get_cluster_from_placement_decisions():
    placement = _lookup_app_resource("placement")
    if not placement:
        return None

    info("Waiting for '%s' decisions", placement)
    kubectl.wait(
        placement,
        "--for=condition=PlacementSatisfied",
        "--timeout=60s",
        context=env["hub"],
        log=debug,
    )

    placement_name = placement.split("/")[1]

    return kubectl.get(
        "placementdecisions",
        f"--selector=cluster.open-cluster-management.io/placement={placement_name}"
        f"--namespace={config['namespace']}",
        "--output=jsonpath={.status.decisions[0].clusterName}",
        context=env["hub"],
    )


def _get_cluster_from_placementrule():
    placementrule = _lookup_app_resource("placementrule")
    if not placementrule:
        return None

    info("Waiting for '%s' decisions", placementrule)
    drenv.wait_for(
        placementrule,
        output="jsonpath={.status.decisions}",
        namespace=config["namespace"],
        timeout=60,
        profile=env["hub"],
        log=debug,
    )

    return kubectl.get(
        placementrule,
        f"--namespace={config['namespace']}",
        "--output=jsonpath={.status.decisions[0].clusterName}",
        context=env["hub"],
    )


def failover():
    """
    Start failover to cluster.
    """
    cluster = target_cluster()
    drpc = _lookup_app_resource("drpc")

    info("Starting failover for '%s' to cluster '%s'", drpc, cluster)
    patch = {"spec": {"action": "Failover", "failoverCluster": cluster}}
    kubectl.patch(
        drpc,
        "--patch",
        json.dumps(patch),
        "--type=merge",
        f"--namespace={config['namespace']}",
        context=env["hub"],
        log=debug,
    )


def relocate():
    """
    Start relocate to cluster.
    """
    cluster = target_cluster()
    drpc = _lookup_app_resource("drpc")

    info("Starting relocate for '%s' to cluster '%s'", drpc, cluster)
    patch = {"spec": {"action": "Relocate", "preferredCluster": cluster}}
    kubectl.patch(
        drpc,
        "--patch",
        json.dumps(patch),
        "--type=merge",
        f"--namespace={config['namespace']}",
        context=env["hub"],
        log=debug,
    )


def wait_for_drpc_status():
    """
    Wait until drpc exists and reports status. This is needed only during
    inital deployment.
    """
    info("waiting for namespace %s", config["namespace"])
    drenv.wait_for(
        f"namespace/{config['namespace']}",
        timeout=60,
        profile=env["hub"],
        log=debug,
    )

    drpc = _lookup_app_resource("drpc")

    info("Waiting until '%s' reports status", drpc)
    drenv.wait_for(
        drpc,
        output="jsonpath={.status}",
        namespace=config["namespace"],
        timeout=60,
        profile=env["hub"],
        log=debug,
    )


def wait_for_drpc_phase(phase):
    drpc = _lookup_app_resource("drpc")

    info("Waiting for '%s' phase '%s'", drpc, phase)
    kubectl.wait(
        drpc,
        f"--for=jsonpath={{.status.phase}}={phase}",
        f"--namespace={config['namespace']}",
        "--timeout=300s",
        context=env["hub"],
        log=debug,
    )


def wait_until_drpc_is_stable(timeout=300):
    """
    Wait until drpc is in stable state:
    - Available=true
    - PeerReady=true
    - lastGroupSyncTime!=''
    """
    drpc = _lookup_app_resource("drpc")

    info("Waiting for '%s' Available condition", drpc)
    kubectl.wait(
        drpc,
        "--for=condition=Available",
        f"--namespace={config['namespace']}",
        f"--timeout={timeout}s",
        context=env["hub"],
        log=debug,
    )

    info("Waiting for '%s' PeerReady condition", drpc)
    kubectl.wait(
        drpc,
        "--for=condition=PeerReady",
        f"--namespace={config['namespace']}",
        f"--timeout={timeout}s",
        context=env["hub"],
        log=debug,
    )

    info("Waiting for '%s' first replication", drpc)
    drenv.wait_for(
        drpc,
        output="jsonpath={.status.lastGroupSyncTime}",
        namespace=config["namespace"],
        timeout=timeout,
        profile=env["hub"],
        log=debug,
    )


def _lookup_app_resource(kind):
    out = kubectl.get(
        kind,
        f"--selector=app={config['name']}",
        f"--namespace={config['namespace']}",
        "--output=name",
        context=env["hub"],
    )
    return out.rstrip()
