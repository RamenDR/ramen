# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import argparse
import json
import logging
import os
import string
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


def deploy(cluster):
    """
    Deploy application on cluster.
    """
    info("Deploying subscription based application")
    yaml = _kustomization(cluster)
    with drenv.kustomization_yaml(yaml) as tmpdir:
        kubectl.apply(
            f"--kustomize={tmpdir}",
            context=env["hub"],
            log=debug,
        )


def undeploy():
    """
    Undeploy an application.
    """
    info("Undeploying subscription based application")
    yaml = _kustomization("unused")
    with drenv.kustomization_yaml(yaml) as tmpdir:
        kubectl.delete(
            "--ignore-not-found",
            f"--kustomize={tmpdir}",
            context=env["hub"],
            log=debug,
        )


def failover(cluster):
    """
    Start failover to cluster.
    """
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


def relocate(cluster):
    """
    Start relocate to cluster.
    """
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


def _kustomization(cluster):
    yaml = """
resources:
  - ${repo}/subscriptions?ref=${branch}
  - ${repo}/subscriptions/busybox?ref=${branch}
patches:
  - target:
      kind: DRPlacementControl
      name: busybox-drpc
    patch: |-
      - op: replace
        path: /spec/preferredCluster
        value: ${cluster}
"""
    template = string.Template(yaml)
    return template.substitute(
        repo=config["repo"],
        branch=config["branch"],
        cluster=cluster,
    )
