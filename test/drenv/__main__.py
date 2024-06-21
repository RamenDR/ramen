# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import argparse
import concurrent.futures
import json
import logging
import os
import shutil
import signal
import sys
import time

from functools import partial

import yaml

import drenv
from . import cache
from . import cluster
from . import commands
from . import containerd
from . import envfile
from . import kubectl
from . import minikube
from . import ramen
from . import shutdown

ADDONS_DIR = "addons"

executors = []


def main():
    args = parse_args()
    logging.basicConfig(
        level=logging.DEBUG if args.verbose else logging.INFO,
        format="%(asctime)s %(levelname)-7s %(message)s",
    )

    signal.signal(signal.SIGTERM, handle_termination_signal)
    become_process_group_leader()
    try:
        args.func(args)
    except Exception:
        logging.exception("Command failed")
        sys.exit(1)
    finally:
        shutdown_executors()
        terminate_process_group()


def parse_args():
    parser = argparse.ArgumentParser(prog="drenv")

    sp = parser.add_subparsers(
        title="commands",
        dest="command",
        required=True,
    )

    start_parser = add_command(sp, "start", do_start, help="start an environment")
    start_parser.add_argument(
        "--skip-tests",
        dest="run_tests",
        action="store_false",
        help="Do not run addons 'test' hooks",
    )
    start_parser.add_argument(
        "--skip-addons",
        dest="run_addons",
        action="store_false",
        help="Do not run addons 'start' hooks",
    )

    stop_parser = add_command(sp, "stop", do_stop, help="stop an environment")
    stop_parser.add_argument(
        "--skip-addons",
        dest="run_addons",
        action="store_false",
        help="Do not run addons 'stop' hooks",
    )

    add_command(sp, "delete", do_delete, help="delete an environment")
    add_command(sp, "suspend", do_suspend, help="suspend virtual machines")
    add_command(sp, "resume", do_resume, help="resume virtual machines")
    add_command(sp, "dump", do_dump, help="dump an environment yaml")
    add_command(sp, "cache", do_cache, help="cache environment resources")

    add_command(sp, "clear", do_clear, help="cleared cached resources", envfile=False)
    add_command(sp, "setup", do_setup, help="setup minikube for drenv", envfile=False)
    add_command(sp, "cleanup", do_cleanup, help="cleanup minikube", envfile=False)

    return parser.parse_args()


def add_command(sp, name, func, help=None, envfile=True):
    parser = sp.add_parser(name, help=help)
    parser.add_argument("-v", "--verbose", action="store_true", help="be more verbose")
    parser.set_defaults(func=func)
    if envfile:
        parser.add_argument(
            "--name-prefix",
            metavar="PREFIX",
            help="prefix profile names",
        )
        parser.add_argument(
            "--max-workers",
            type=int,
            metavar="N",
            help="maximum number of workers per profile",
        )
        parser.add_argument("filename", help="path to environment file")
    return parser


def load_env(args):
    with open(args.filename) as f:
        return envfile.load(f, name_prefix=args.name_prefix)


def shutdown_executors():
    # Prevents adding new executors, starting new child processes, and aborts
    # running workers.
    shutdown.start()

    # Cancels pending tasks and prevent submission of new tasks. This does not
    # affect running tasks, they will be aborted when they check shutdown
    # status, or when the child process terminates.
    for name, executor in executors:
        logging.debug("[main] Shutting down executor %s", name)
        executor.shutdown(wait=False, cancel_futures=True)


def add_executor(name, executor):
    with shutdown.guard():
        logging.debug("[main] Add executor %s", name)
        executors.append((name, executor))


def become_process_group_leader():
    """
    To allow cleaning up after errors, ensure that we are a process group
    leader, so we can terminate the process group during shutdown.
    """
    if os.getpid() != os.getpgid(0):
        os.setpgid(0, 0)
        logging.debug("[main] Created new process group %s", os.getpgid(0))


def terminate_process_group():
    """
    Terminate all child processes and child processes created by them.
    """
    logging.debug("[main] Terminating process group %s", os.getpid())
    signal.signal(signal.SIGHUP, signal.SIG_IGN)
    os.killpg(0, signal.SIGHUP)


def handle_termination_signal(signo, frame):
    logging.info("[main] Terminated by signal %s", signo)
    sys.exit(1)


def do_setup(args):
    logging.info("[main] Setting up minikube for drenv")
    minikube.setup_files()


def do_cleanup(args):
    logging.info("[main] Cleaning up minikube")
    minikube.cleanup_files()


def do_clear(args):
    logging.info("[main] Clearing cache")
    cache.clear()


def do_cache(args):
    env = load_env(args)
    start = time.monotonic()
    logging.info("[%s] Refreshing cached addons", env["name"])
    addons = collect_addons(env)
    execute(
        cache_addon,
        addons,
        "cache",
        max_workers=args.max_workers,
        ctx=env["name"],
    )
    logging.info(
        "[%s] Cached addons refreshed in %.2f seconds",
        env["name"],
        time.monotonic() - start,
    )


def do_start(args):
    env = load_env(args)
    start = time.monotonic()
    logging.info("[%s] Starting environment", env["name"])

    hooks = []
    if args.run_addons:
        hooks.append("start")
    if args.run_tests:
        hooks.append("test")

    execute(
        start_cluster,
        env["profiles"],
        "profiles",
        hooks=hooks,
        args=args,
    )

    if hooks:
        execute(run_worker, env["workers"], "workers", hooks=hooks)

    if "ramen" in env:
        ramen.dump_e2e_config(env)

    logging.info(
        "[%s] Environment started in %.2f seconds",
        env["name"],
        time.monotonic() - start,
    )


def do_stop(args):
    env = load_env(args)
    start = time.monotonic()
    logging.info("[%s] Stopping environment", env["name"])
    hooks = ["stop"] if args.run_addons else []
    execute(stop_cluster, env["profiles"], "profiles", hooks=hooks)
    logging.info(
        "[%s] Environment stopped in %.2f seconds",
        env["name"],
        time.monotonic() - start,
    )


def do_delete(args):
    env = load_env(args)
    start = time.monotonic()
    logging.info("[%s] Deleting environment", env["name"])
    execute(delete_cluster, env["profiles"], "profiles")

    env_config = drenv.config_dir(env["name"])
    if os.path.exists(env_config):
        logging.info("[%s] Removing config %s", env["name"], env_config)
        shutil.rmtree(env_config)

    logging.info(
        "[%s] Environment deleted in %.2f seconds",
        env["name"],
        time.monotonic() - start,
    )


def do_suspend(args):
    env = load_env(args)
    logging.info("[%s] Suspending environment", env["name"])
    for profile in env["profiles"]:
        run("virsh", "-c", "qemu:///system", "suspend", profile["name"])


def do_resume(args):
    env = load_env(args)
    logging.info("[%s] Resuming environment", env["name"])
    for profile in env["profiles"]:
        run("virsh", "-c", "qemu:///system", "resume", profile["name"])


def do_dump(args):
    env = load_env(args)
    yaml.dump(env, sys.stdout)


def execute(func, profiles, name, max_workers=None, **options):
    """
    Execute func in parallel for every profile.

    func is invoked with profile and **options. It must have this signature:

        def func(profile, **options):

    """
    executor = concurrent.futures.ThreadPoolExecutor(max_workers=max_workers)
    try:
        add_executor(name, executor)
        futures = {}
        for p in profiles:
            futures[executor.submit(func, p, **options)] = p["name"]

        for f in concurrent.futures.as_completed(futures):
            # If the future failed, stop waiting for the rest of the futures
            # and let the error propagate to the top level error handler.
            f.result()
    finally:
        executor.shutdown(wait=False, cancel_futures=True)


def collect_addons(env):
    found = {}
    for profile in env["profiles"]:
        for worker in profile["workers"]:
            for addon in worker["addons"]:
                found[addon["name"]] = addon
    for worker in env["workers"]:
        for addon in worker["addons"]:
            found[addon["name"]] = addon
    return found.values()


def start_cluster(profile, hooks=(), args=None, **options):
    if profile["external"]:
        logging.debug("[%s] Skipping external cluster", profile["name"])
    else:
        is_restart = minikube_profile_exists(profile["name"])
        start_minikube_cluster(profile, verbose=args.verbose)
        if profile["containerd"]:
            logging.info("[%s] Configuring containerd", profile["name"])
            containerd.configure(profile)
        if is_restart:
            restart_failed_deployments(profile)
        else:
            minikube.load_files(profile["name"])

    if hooks:
        execute(
            run_worker,
            profile["workers"],
            profile["name"],
            max_workers=args.max_workers,
            hooks=hooks,
        )


def stop_cluster(profile, hooks=(), **options):
    cluster_status = cluster.status(profile["name"])

    if cluster_status == cluster.READY and hooks:
        execute(
            run_worker,
            profile["workers"],
            profile["name"],
            hooks=hooks,
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
        feature_gates=profile["feature_gates"],
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


def restart_failed_deployments(profile, initial_wait=30):
    """
    When restarting, kubectl can report stale status for a while, before it
    starts to report real status. Then it takes a while until all deployments
    become available.

    We first wait for initial_wait seconds to give Kubernetes chance to fail
    liveness and readiness checks. Then we restart for failed deployments.
    """
    logging.info("[%s] Waiting for fresh status", profile["name"])
    time.sleep(initial_wait)

    logging.info("[%s] Looking up failed deployments", profile["name"])
    debug = partial(logging.debug, f"[{profile['name']}] %s")

    for namespace, deploy, progressing in failed_deployments(profile):
        logging.info(
            "[%s] Restarting failed deployment '%s': %s",
            profile["name"],
            deploy,
            progressing["message"],
        )
        kubectl.rollout(
            "restart",
            deploy,
            f"--namespace={namespace}",
            context=profile["name"],
            log=debug,
        )


def failed_deployments(profile):
    out = kubectl.get(
        "namespace",
        "--output=jsonpath={.items[*].metadata.name}",
        context=profile["name"],
    )
    for namespace in out.split():
        names = kubectl.get(
            "deploy",
            f"--namespace={namespace}",
            "--output=name",
            context=profile["name"],
        )
        for deploy in names.splitlines():
            out = kubectl.get(
                deploy,
                f"--namespace={namespace}",
                "--output=jsonpath={.status.conditions[?(@.type=='Progressing')]}",
                context=profile["name"],
            )
            progressing = json.loads(out)
            if progressing["status"] == "False":
                yield namespace, deploy, progressing


def run_worker(worker, hooks=(), reverse=False, allow_failure=False):
    addons = reversed(worker["addons"]) if reverse else worker["addons"]
    for addon in addons:
        run_addon(addon, worker["name"], hooks=hooks, allow_failure=allow_failure)


def cache_addon(addon, ctx="global"):
    addon_dir = os.path.join(ADDONS_DIR, addon["name"])
    if not os.path.isdir(addon_dir):
        skip_addon(addon, ctx)
        return

    hook = os.path.join(addon_dir, "cache")
    if os.path.isfile(hook):
        run_hook(hook, (), ctx)


def run_addon(addon, name, hooks=(), allow_failure=False):
    addon_dir = os.path.join(ADDONS_DIR, addon["name"])
    if not os.path.isdir(addon_dir):
        skip_addon(addon, name)
        return

    for filename in hooks:
        hook = os.path.join(addon_dir, filename)
        if os.path.isfile(hook):
            run_hook(hook, addon["args"], name, allow_failure=allow_failure)


def skip_addon(addon, ctx):
    logging.warning(
        "[%s] Addon '%s' does not exist - skipping",
        ctx,
        addon["name"],
    )


def run_hook(hook, args, name, allow_failure=False):
    if shutdown.started():
        logging.debug("[%s] Shutting down", name)
        raise shutdown.Started

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
