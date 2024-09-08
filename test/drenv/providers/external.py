# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import logging
import time
from functools import partial

from drenv import cluster

# Provider scope


def setup():
    logging.info("[external] Skipping setup for external provider")


def cleanup():
    logging.info("[external] Skipping cleanup for external provider")


# Cluster scope


def exists(profile):
    return True


def start(profile, verbose=False):
    start = time.monotonic()
    logging.info("[%s] Checking external cluster status", profile["name"])

    # Fail fast if cluster is not configured, we cannot recover from this.
    status = cluster.status(profile["name"])
    if status == cluster.UNKNOWN:
        raise RuntimeError(f"Cluster '{profile['name']}' does not exist")

    # Otherwise handle temporary outage gracefuly.
    debug = partial(logging.debug, f"[{profile['name']}] %s")
    cluster.wait_until_ready(profile["name"], timeout=60, log=debug)

    logging.info(
        "[%s] Cluster ready in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )


def configure(profile, existing=False):
    logging.info("[%s] Skipping configure for external cluster", profile["name"])


def stop(profile):
    logging.info("[%s] Skipping stop for external cluster", profile["name"])


def delete(profile):
    logging.info("[%s] Skipping delete for external cluster", profile["name"])


def load(profile, image):
    logging.info("[%s] Skipping load image for external cluster", profile["name"])


def suspend(profile):
    logging.info("[%s] Skipping suspend for external cluster", profile["name"])


def resume(profile):
    logging.info("[%s] Skipping resume for external cluster", profile["name"])


def cp(name, src, dst):
    logging.warning("[%s] cp not implemented yet for external cluster", name)


def ssh(name, command):
    logging.warning("[%s] ssh not implemented yet for external cluster", name)
