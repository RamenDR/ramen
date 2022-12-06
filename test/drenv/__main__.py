# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import argparse
import concurrent.futures
import logging
import os
import shutil
import sys
import time

import yaml

import drenv
from . import commands
from . import envfile

CMD_PREFIX = "cmd_"


def main():
    commands = [n[len(CMD_PREFIX) :] for n in globals() if n.startswith(CMD_PREFIX)]

    p = argparse.ArgumentParser(prog="drenv")
    p.add_argument("-v", "--verbose", action="store_true", help="Be more verbose")
    p.add_argument("command", choices=commands, help="Command to run")
    p.add_argument("--name-prefix", help="Prefix profile names")
    p.add_argument("filename", help="Environment filename")
    args = p.parse_args()

    logging.basicConfig(
        level=logging.DEBUG if args.verbose else logging.INFO,
        format="%(asctime)s %(levelname)-7s %(message)s",
    )

    with open(args.filename) as f:
        env = envfile.load(f, name_prefix=args.name_prefix)

    func = globals()[CMD_PREFIX + args.command]
    func(env)


def cmd_start(env):
    start = time.monotonic()
    logging.info("[%s] Starting environment", env["name"])
    # Delaying `minikube start` ensures cluster start order.
    execute(start_cluster, env["profiles"], delay=1)
    execute(run_worker, env["workers"])
    logging.info(
        "[%s] Environment started in %.2f seconds",
        env["name"],
        time.monotonic() - start,
    )


def cmd_stop(env):
    start = time.monotonic()
    logging.info("[%s] Stopping environment", env["name"])
    execute(stop_cluster, env["profiles"])
    logging.info(
        "[%s] Environment stopped in %.2f seconds",
        env["name"],
        time.monotonic() - start,
    )


def cmd_delete(env):
    start = time.monotonic()
    logging.info("[%s] Deleting environment", env["name"])
    execute(delete_cluster, env["profiles"])
    logging.info(
        "[%s] Environment deleted in %.2f seconds",
        env["name"],
        time.monotonic() - start,
    )


def cmd_dump(env):
    yaml.dump(env, sys.stdout)


def execute(func, profiles, delay=0):
    failed = False

    with concurrent.futures.ThreadPoolExecutor() as e:
        futures = {}

        for p in profiles:
            futures[e.submit(func, p)] = p["name"]
            time.sleep(delay)

        for f in concurrent.futures.as_completed(futures):
            try:
                f.result()
            except Exception:
                logging.exception("[%s] Cluster failed", futures[f])
                failed = True

    if failed:
        sys.exit(1)


def start_cluster(profile):
    start = time.monotonic()
    logging.info("[%s] Starting cluster", profile["name"])

    is_restart = drenv.cluster_exists(profile["name"])

    minikube(
        "start",
        "--driver",
        "kvm2",
        "--container-runtime",
        profile["container_runtime"],
        "--extra-disks",
        str(profile["extra_disks"]),
        "--disk-size",
        profile["disk_size"],
        "--network",
        profile["network"],
        "--nodes",
        str(profile["nodes"]),
        "--cni",
        profile["cni"],
        "--cpus",
        str(profile["cpus"]),
        "--memory",
        profile["memory"],
        "--addons",
        ",".join(profile["addons"]),
        profile=profile["name"],
    )

    logging.info(
        "[%s] Cluster started in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )

    if is_restart:
        wait_for_deployments(profile)

    execute(run_worker, profile["workers"])


def stop_cluster(profile):
    start = time.monotonic()
    logging.info("[%s] Stopping cluster", profile["name"])
    minikube("stop", profile=profile["name"])
    logging.info(
        "[%s] Cluster stopped in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )


def delete_cluster(profile):
    start = time.monotonic()
    logging.info("[%s] Deleting cluster", profile["name"])
    minikube("delete", profile=profile["name"])
    profile_config = drenv.config_dir(profile["name"])
    if os.path.exists(profile_config):
        logging.info("[%s] Removing config %s", profile["name"], profile_config)
        shutil.rmtree(profile_config)
    logging.info(
        "[%s] Cluster deleted in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )


def wait_for_deployments(profile, initial_wait=30, timeout=300):
    """
    When restarting, kubectl can report stale status for a while, before it
    starts to report real status. Then it takes a while until all deployments
    become available.

    We first sleep for initial_wait seconds, to give Kubernetes chance to fail
    liveness and readiness checks, and then wait until all deployments are
    available or the timeout has expired.

    TODO: Check if there is more reliable way to wait for actual status.
    """
    start = time.monotonic()
    logging.info(
        "[%s] Waiting until all deployments are available",
        profile["name"],
    )

    time.sleep(initial_wait)

    kubectl(
        "wait",
        "deploy",
        "--all",
        "--for",
        "condition=available",
        "--all-namespaces",
        "--timeout",
        f"{timeout}s",
        profile=profile["name"],
    )

    logging.info(
        "[%s] Deployments are available in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )


def kubectl(cmd, *args, profile=None):
    minikube("kubectl", "--", cmd, *args, profile=profile)


def minikube(cmd, *args, profile=None):
    run("minikube", cmd, "--profile", profile, *args, name=profile)


def run_worker(worker):
    for script in worker["scripts"]:
        run_script(script, worker["name"])


def run_script(script, name):
    for filename in "start", "test":
        hook = os.path.join(script["name"], filename)
        if os.path.isfile(hook):
            run_hook(hook, script["args"], name)


def run_hook(hook, args, name):
    start = time.monotonic()
    logging.info("[%s] Running %s", name, hook)
    run(hook, *args, name=name)
    logging.info(
        "[%s] %s completed in %.2f seconds", name, hook, time.monotonic() - start
    )


def run(*cmd, name=None):
    for line in commands.watch(*cmd):
        logging.debug("[%s] %s", name, line)


if __name__ == "__main__":
    main()
