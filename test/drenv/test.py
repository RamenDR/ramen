# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import argparse
import json
import logging
import os
import sys

import drenv
from drenv import kubectl

from . import ramen
from . import yaml


workdir = None
env = None
config = None
log = None
parser = None
log_format = "%(asctime)s %(levelname)-7s [%(name)s] %(message)s"

OCM_SCHEDULING_DISABLE = (
    "cluster.open-cluster-management.io/experimental-scheduling-disable"
)


def start(name, file):
    global workdir, parser, log

    # Setting up logging and sys.excepthook must be first so any failure will
    # be reported using the logger.
    log = logging.getLogger(name)
    sys.excepthook = _excepthook

    # We start with info level. After parsing the command line we may change
    # the level to debug.
    logging.basicConfig(level=logging.INFO, format=log_format)

    # Working directory for runing the test.
    workdir = os.path.abspath(os.path.dirname(file))

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
        "-c",
        "--config",
        default=os.path.join(workdir, "config.yaml"),
        help="Test configuration file",
    )
    parser.add_argument(
        "filename",
        help="Environment filename",
    )


def add_argument(*args, **kw):
    parser.add_argument(*args, **kw)


def parse_args():
    global config, env

    args = parser.parse_args()
    if args.verbose:
        log.setLevel(logging.DEBUG)
    debug("Parsed arguments: %s", args)

    with open(args.config) as f:
        config = yaml.safe_load(f)

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
    info("Deploying application '%s'", config["name"])
    kubectl.apply(
        f"--kustomize={config['repo']}/{config['path']}?ref={config['branch']}",
        context=env["hub"],
        log=debug,
    )


def undeploy():
    """
    Undeploy an application.
    """
    info("Undeploying application '%s'", config["name"])
    kubectl.delete(
        f"--kustomize={config['repo']}/{config['path']}?ref={config['branch']}",
        "--ignore-not-found",
        context=env["hub"],
        log=debug,
    )


def enable_dr():
    """
    Enable DR for deployed application.
    """
    placement = _lookup_app_resource("placement")
    if not placement:
        raise RuntimeError(f"Cannot find placement for application {config['name']}")

    info("Disabling OCM scheduling for '%s'", placement)
    kubectl.annotate(
        placement,
        {OCM_SCHEDULING_DISABLE: "true"},
        namespace=config["namespace"],
        context=env["hub"],
        log=debug,
    )

    cluster = lookup_cluster()
    placement_name = placement.split("/")[1]
    consistency_groups = env["features"].get("consistency_groups", False)
    cg_enabled = "true" if consistency_groups else "false"

    drpc = f"""
apiVersion: ramendr.openshift.io/v1alpha1
kind: DRPlacementControl
metadata:
  name: {config['name']}-drpc
  namespace: {config['namespace']}
  annotations:
    drplacementcontrol.ramendr.openshift.io/is-cg-enabled: '{cg_enabled}'
  labels:
    app: {config['name']}
spec:
  preferredCluster: {cluster}
  drPolicyRef:
    name: {config['dr_policy']}
  placementRef:
    kind: Placement
    name: {placement_name}
  pvcSelector:
    matchLabels:
      appname: {config['pvc_label']}
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
    placement = _lookup_app_resource("placement")
    if not placement:
        raise RuntimeError(f"Cannot find placement for application {config['name']}")

    info("Waiting for '%s' decisions", placement)
    kubectl.wait(
        placement,
        "--for=condition=PlacementSatisfied",
        "--timeout=60s",
        f"--namespace={config['namespace']}",
        context=env["hub"],
        log=debug,
    )

    placement_name = placement.split("/")[1]

    return kubectl.get(
        "placementdecisions",
        f"--selector=cluster.open-cluster-management.io/placement={placement_name}",
        f"--namespace={config['namespace']}",
        "--output=jsonpath={.items[0].status.decisions[0].clusterName}",
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
