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


def failover(cluster):
    """
    Start failover to cluster.
    """
    info("Starting failover to cluster '%s'", cluster)
    patch = {"spec": {"action": "Failover", "failoverCluster": cluster}}
    kubectl.patch(
        f"drpc/{config['name']}",
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
    info("Starting relocate to cluster '%s'", cluster)
    patch = {"spec": {"action": "Relocate", "preferredCluster": cluster}}
    kubectl.patch(
        f"drpc/{config['name']}",
        "--patch",
        json.dumps(patch),
        "--type=merge",
        f"--namespace={config['namespace']}",
        context=env["hub"],
        log=debug,
    )


def wait_for_drpc_phase(phase):
    info("Waiting for drpc '%s' phase", phase)
    kubectl.wait(
        f"drpc/{config['name']}",
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
    info("Waiting for Available condition")
    kubectl.wait(
        f"drpc/{config['name']}",
        "--for=condition=Available",
        f"--namespace={config['namespace']}",
        f"--timeout={timeout}s",
        context=env["hub"],
        log=debug,
    )

    info("Waiting for PeerReady condition")
    kubectl.wait(
        f"drpc/{config['name']}",
        "--for=condition=PeerReady",
        f"--namespace={config['namespace']}",
        f"--timeout={timeout}s",
        context=env["hub"],
        log=debug,
    )

    info("Waiting for first replication")
    drenv.wait_for(
        f"drpc/{config['name']}",
        output="jsonpath={.status.lastGroupSyncTime}",
        namespace=config["namespace"],
        timeout=timeout,
        profile=env["hub"],
        log=debug,
    )
