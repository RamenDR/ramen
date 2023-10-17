# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import argparse
import concurrent.futures
import json
import logging
import os
import shutil
import sys
import time

import yaml

import drenv
from . import cluster
from . import commands
from . import containerd
from . import envfile
from . import minikube
from . import ramen

CMD_PREFIX = "cmd_"
ADDONS_DIR = "addons"


def main():
    commands = [n[len(CMD_PREFIX) :] for n in globals() if n.startswith(CMD_PREFIX)]

    p = argparse.ArgumentParser(prog="drenv")
    p.add_argument("-v", "--verbose", action="store_true", help="Be more verbose")
    p.add_argument(
        "--skip-tests", dest="run_tests", action="store_false", help="Skip self tests"
    )
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
    func(env, args)


def cmd_start(env, args):
    start = time.monotonic()
    logging.info("[%s] Starting environment", env["name"])
    hooks = ["start", "test"] if args.run_tests else ["start"]

    # Delaying `minikube start` ensures cluster start order.
    execute(
        start_cluster,
        env["profiles"],
        delay=1,
        hooks=hooks,
        verbose=args.verbose,
    )
    execute(run_worker, env["workers"], hooks=hooks)

    if "ramen" in env:
        ramen.dump_e2e_config(env)

    logging.info(
        "[%s] Environment started in %.2f seconds",
        env["name"],
        time.monotonic() - start,
    )


def cmd_stop(env, args):
    start = time.monotonic()
    logging.info("[%s] Stopping environment", env["name"])
    execute(stop_cluster, env["profiles"])
    logging.info(
        "[%s] Environment stopped in %.2f seconds",
        env["name"],
        time.monotonic() - start,
    )


def cmd_delete(env, args):
    start = time.monotonic()
    logging.info("[%s] Deleting environment", env["name"])
    execute(delete_cluster, env["profiles"])

    env_config = drenv.config_dir(env["name"])
    if os.path.exists(env_config):
        logging.info("[%s] Removing config %s", env["name"], env_config)
        shutil.rmtree(env_config)

    logging.info(
        "[%s] Environment deleted in %.2f seconds",
        env["name"],
        time.monotonic() - start,
    )


def cmd_dump(env, args):
    yaml.dump(env, sys.stdout)


def execute(func, profiles, delay=0, **options):
    """
    Execute func in parallel for every profile.

    func is invoked with profile and **options. It must have this signature:

        def func(profile, **options):

    """
    failed = False

    with concurrent.futures.ThreadPoolExecutor() as e:
        futures = {}

        for p in profiles:
            futures[e.submit(func, p, **options)] = p["name"]
            time.sleep(delay)

        for f in concurrent.futures.as_completed(futures):
            try:
                f.result()
            except Exception:
                logging.exception("[%s] Cluster failed", futures[f])
                failed = True

    if failed:
        sys.exit(1)


def start_cluster(profile, hooks=(), verbose=False, **options):
    if profile["external"]:
        logging.debug("[%s] Skipping external cluster", profile["name"])
    else:
        is_restart = minikube_profile_exists(profile["name"])
        start_minikube_cluster(profile, verbose=verbose)
        if profile["containerd"]:
            logging.info("[%s] Configuring containerd", profile["name"])
            containerd.configure(profile)
        if is_restart:
            wait_for_deployments(profile)

    execute(run_worker, profile["workers"], hooks=hooks)


def stop_cluster(profile, **options):
    cluster_status = cluster.status(profile["name"])

    if cluster_status == cluster.READY:
        execute(
            run_worker,
            profile["workers"],
            hooks=["stop"],
            reverse=True,
            allow_failure=True,
        )

    if profile["external"]:
        logging.debug("[%s] Skipping external cluster", profile["name"])
    elif cluster_status != cluster.UNKNOWN:
        stop_minikube_cluster(profile)


def delete_cluster(profile, **options):
    if profile["external"]:
        logging.debug("[%s] Skipping external cluster", profile["name"])
    else:
        delete_minikube_cluster(profile)

    profile_config = drenv.config_dir(profile["name"])
    if os.path.exists(profile_config):
        logging.info("[%s] Removing config %s", profile["name"], profile_config)
        shutil.rmtree(profile_config)


def minikube_profile_exists(name):
    out = minikube.profile("list", output="json")
    profiles = json.loads(out)
    for profile in profiles["valid"]:
        if profile["Name"] == name:
            return True
    return False


def start_minikube_cluster(profile, verbose=False):
    start = time.monotonic()
    logging.info("[%s] Starting minikube cluster", profile["name"])

    minikube.start(
        profile["name"],
        driver=profile["driver"],
        container_runtime=profile["container_runtime"],
        extra_disks=profile["extra_disks"],
        disk_size=profile["disk_size"],
        network=profile["network"],
        nodes=profile["nodes"],
        cni=profile["cni"],
        cpus=profile["cpus"],
        memory=profile["memory"],
        addons=profile["addons"],
        service_cluster_ip_range=profile["service_cluster_ip_range"],
        extra_config=profile["extra_config"],
        alsologtostderr=verbose,
    )

    logging.info(
        "[%s] Cluster started in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )


def stop_minikube_cluster(profile):
    start = time.monotonic()
    logging.info("[%s] Stopping cluster", profile["name"])
    minikube.stop(profile["name"])
    logging.info(
        "[%s] Cluster stopped in %.2f seconds",
        profile["name"],
        time.monotonic() - start,
    )


def delete_minikube_cluster(profile):
    start = time.monotonic()
    logging.info("[%s] Deleting cluster", profile["name"])
    minikube.delete(profile["name"])
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


def kubectl(*args, profile=None):
    run("kubectl", "--context", profile, *args, name=profile)


def run_worker(worker, hooks=(), reverse=False, allow_failure=False):
    addons = reversed(worker["addons"]) if reverse else worker["addons"]
    for addon in addons:
        run_addon(addon, worker["name"], hooks=hooks, allow_failure=allow_failure)


def run_addon(addon, name, hooks=(), allow_failure=False):
    addon_dir = os.path.join(ADDONS_DIR, addon["name"])
    if not os.path.isdir(addon_dir):
        logging.warning(
            "[%s] Addon '%s' does not exist - skipping",
            name,
            addon["name"],
        )
        return

    for filename in hooks:
        hook = os.path.join(addon_dir, filename)
        if os.path.isfile(hook):
            run_hook(hook, addon["args"], name, allow_failure=allow_failure)


def run_hook(hook, args, name, allow_failure=False):
    start = time.monotonic()
    logging.info("[%s] Running %s", name, hook)
    try:
        run(hook, *args, name=name)
    except Exception as e:
        if not allow_failure:
            raise
        logging.warning("[%s] %s failed: %s", name, hook, e)
    else:
        logging.info(
            "[%s] %s completed in %.2f seconds",
            name,
            hook,
            time.monotonic() - start,
        )


def run(*cmd, name=None):
    for line in commands.watch(*cmd):
        logging.debug("[%s] %s", name, line)


if __name__ == "__main__":
    main()
