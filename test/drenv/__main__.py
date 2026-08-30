# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import argcomplete
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

import drenv
from . import cache
from . import cluster
from . import commands
from . import envfile
from . import git
from . import kubectl
from . import options
from . import providers
from . import ramen
from . import registry
from . import shutdown
from . import ssh
from . import stress
from . import yaml

ADDONS_DIR = "addons"
LOGFILE = "drenv.log"

executors = []
width = options.terminal_width()


def main():
    args = parse_args()
    configure_logging(args)
    try:
        git_info = git.info()
        logging.debug(
            "[main] Using git repo branch=%s commit=%s dirty=%s",
            git_info["branch"],
            git_info["commit"],
            git_info["dirty"],
        )
    except commands.Error as e:
        logging.debug("[main] git info not available: %s", e)
    signal.signal(signal.SIGTERM, handle_termination_signal)
    become_process_group_leader()
    try:
        args.func(args)
    except Exception:
        logging.exception("Command failed")
        sys.exit(1)
    finally:
        if shutdown_executors():
            terminate_process_group()


def parse_args():
    parser = argparse.ArgumentParser(prog="drenv")

    sp = parser.add_subparsers(
        title="commands",
        dest="command",
        required=True,
    )

    # Environment commands.

    p = add_command(
        sp,
        "start",
        do_start,
        help="start an environment",
        description=options.dedent("""
            Start the DR environment defined in the envfile. Creates the
            clusters if needed, deploys all addons, and runs addon tests.
            The command is idempotent - rerunning it on an already-running
            environment is safe. Run "drenv setup" once before the first
            start to prepare the host.

            Examples:
              # Start the regional-dr environment
              drenv start envs/regional-dr.yaml
            """),
    )
    p.add_argument(
        "--skip-tests",
        dest="run_tests",
        action="store_false",
        help="Do not run addons 'test' hooks",
    )
    p.add_argument(
        "--skip-addons",
        dest="run_addons",
        action="store_false",
        help="Do not run addons 'start' hooks",
    )
    p.add_argument(
        "--max-workers",
        type=int,
        metavar="N",
        help="maximum number of workers per profile",
    )
    p.add_argument(
        "--timeout",
        type=int,
        help="time in seconds to wait until clsuter is started",
    )
    p.add_argument(
        "--local-registry",
        action="store_true",
        help=(
            "Use local registry. For lima provider the local registry must "
            "have the k8s images for kubeadm. See registry/README.md for "
            "more info."
        ),
    )
    p.add_argument(
        "--dns-mode",
        choices=["auto", "static", "host"],
        default="auto",
        help=(
            "DNS configuration mode. 'auto' detects managed Macs and uses "
            "'static' if needed. 'static' configures public DNS servers "
            "(8.8.8.8, 1.1.1.1). 'host' uses the host resolver (default for "
            "minikube, may not work on managed Macs)."
        ),
    )

    p = add_command(
        sp,
        "stop",
        do_stop,
        help="stop an environment",
        description=options.dedent("""
            Stop the DR environment defined in the envfile. Stops all
            clusters for the environment profiles. Cluster data
            is preserved and can be resumed later with "drenv start".

            Examples:
              # Stop the regional-dr environment
              drenv stop envs/regional-dr.yaml
            """),
    )
    p.add_argument(
        "--skip-addons",
        dest="run_addons",
        action="store_false",
        help="Do not run addons 'stop' hooks",
    )

    p = add_command(
        sp,
        "cache",
        do_cache,
        help="cache environment resources",
        description=options.dedent("""
            Pre-fetch and cache addon resources for the envfile. Runs each
            addon's "cache" hook to prepare resources needed by the
            environment. This improves stability when running with
            bad network connectivity.

            Examples:
              # Cache resources for the regional-dr environment
              drenv cache envs/regional-dr.yaml
            """),
    )
    p.add_argument(
        "--max-workers",
        type=int,
        metavar="N",
        help="maximum number of workers per profile",
    )

    p = add_command(
        sp,
        "gather",
        do_gather,
        help="gather environment data",
        description=options.dedent("""
            Gather diagnostic data from a running environment. Collects
            logs and resource dumps from every cluster defined in the
            envfile, useful for debugging.

            Examples:
              # Gather data from all namespaces into the default directory
              drenv gather --directory rdr.gather envs/regional-dr.yaml
            """),
    )
    p.add_argument(
        "-d",
        "--directory",
        help='directory for storing gathered data (default "gather.{timestamp}")',
    )
    p.add_argument(
        "-n",
        "--namespaces",
        type=lambda s: s.split(","),
        help="if specified, comma separated list of namespaces to gather data from",
    )

    p = add_command(
        sp,
        "load",
        do_load,
        help="load an image into the cluster",
        description=options.dedent("""
            Load a container image into every cluster in the envfile.
            Useful for testing local image builds without pushing them to a
            registry first. Use "podman save -o myimage.tar myimage" to
            create the image tarball.

            Examples:
              # Load a local image tarball into all profiles
              drenv load --image myimage.tar envs/regional-dr.yaml
            """),
    )
    p.add_argument(
        "--image",
        required=True,
        help="image to load into the cluster in tar format",
    )

    add_command(
        sp,
        "delete",
        do_delete,
        help="delete an environment",
        description=options.dedent("""
            Delete a DR environment. Deletes all clusters and
            temporary files under `~/.config/drenv` for the
            profiles and environment.

            Examples:
              # Delete the regional-dr environment
              drenv delete envs/regional-dr.yaml
            """),
    )
    add_command(
        sp,
        "suspend",
        do_suspend,
        help="suspend virtual machines",
        description=options.dedent("""
            Suspend the virtual machines for the envfile. Pauses the
            underlying virtual machines for every profile without deleting
            them, freeing up host resources temporarily. Resume them later
            with "drenv resume". Supported on Linux only.

            Examples:
              # Suspend the regional-dr environment
              drenv suspend envs/regional-dr.yaml
            """),
    )
    add_command(
        sp,
        "resume",
        do_resume,
        help="resume virtual machines",
        description=options.dedent("""
            Resume virtual machines previously suspended for the envfile.
            Supported on Linux only.

            Examples:
              # Resume the regional-dr environment
              drenv resume envs/regional-dr.yaml
            """),
    )
    add_command(
        sp,
        "dump",
        do_dump,
        help="dump an environment yaml",
        description=options.dedent("""
            Dump the resolved environment as yaml. Prints the fully
            resolved environment configuration (after applying defaults and
            the name prefix) to stdout. Useful for debugging envfile
            definitions.

            Examples:
              # Dump the resolved regional-dr environment
              drenv dump envs/regional-dr.yaml
            """),
    )

    # Host commands.

    add_command(
        sp,
        "clear",
        do_clear,
        help="clear cached resources",
        envfile=False,
        description=options.dedent("""
            Clear all cached resources. Removes resources cached by
            "drenv cache", freeing up disk space. This affects all
            environments, not just one.

            Examples:
              # Clear all cached resources
              drenv clear
            """),
    )
    add_command(
        sp,
        "setup",
        do_setup,
        help="setup host for drenv",
        description=options.dedent("""
            Prepare the host for the DR environment defined in the envfile.
            Configures the host-level dependencies required by the environment.
            This command does not create or start any clusters. Run it once
            before the first "drenv start", and again after every reboot since
            registry cache containers started by setup do not survive a reboot.

            Examples:
              # Prepare the host for the regional-dr environment
              drenv setup envs/regional-dr.yaml
            """),
    )
    add_command(
        sp,
        "cleanup",
        do_cleanup,
        help="cleanup host",
        description=options.dedent("""
            Clean up host-level dependencies installed by "drenv setup".
            Reverses the changes made by "drenv setup" for the given
            envfile, removing host-level dependencies that are no longer
            needed.

            Examples:
              # Clean up host dependencies for the regional-dr environment
              drenv cleanup envs/regional-dr.yaml
            """),
    )

    # Sub commands.

    add_registry_cache_command(sp)
    add_stress_test_command(sp)

    argcomplete.autocomplete(parser)
    return parser.parse_args()


def add_registry_cache_command(sp):
    p = sp.add_parser(
        "registry-cache",
        help="manage registry cache",
        description=options.wrap_description(
            options.dedent("""
            Manage the drenv registry cache. Inspect or remove cached
            registry containers used by drenv.
            """),
            width,
        ),
        formatter_class=options.formatter_class(width),
    )
    sp = p.add_subparsers(dest="command", required=True)

    p = add_command(
        sp,
        "stats",
        registry.show_stats,
        help="show cache statistics",
        envfile=False,
        description=options.dedent("""
            Display the current contents and usage statistics for the
            cached registry containers managed by drenv.

            Examples:
              # Show cache statistics in JSON format
              drenv registry-cache stats
            """),
    )
    p.add_argument(
        "-o",
        "--output",
        choices=["json", "markdown"],
        default="json",
    )

    add_command(
        sp,
        "remove",
        registry.remove_containers,
        help="remove cache containers",
        envfile=False,
        description=options.dedent("""
            Remove the cached registry containers managed by drenv.

            Examples:
              # Remove all cached registry containers
              drenv registry-cache remove
            """),
    )


def add_stress_test_command(sp):
    p = sp.add_parser(
        "stress-test",
        help="run drenv stress test",
        description=options.wrap_description(
            options.dedent("""
            Run drenv stress tests. Execute repeated stress-test runs
            and collect results for later comparison and reporting.
            """),
            width,
        ),
        formatter_class=options.formatter_class(width),
    )
    sp = p.add_subparsers(dest="command", required=True)

    p = add_command(
        sp,
        "run",
        stress.run,
        help="run stress test",
        description=options.dedent("""
            Execute one or more stress-test runs and write the results to
            the specified output directory.

            Examples:
              # Run a single stress test for the regional-dr environment
              drenv stress-test run -o out/test envs/regional-dr.yaml
            """),
    )
    p.add_argument(
        "-r",
        "--runs",
        type=int,
        default=1,
        help="number of runs (default 1)",
    )
    p.add_argument(
        "-o",
        "--outdir",
        default="out",
        help="directroy for storing test output (default out)",
    )
    p.add_argument(
        "-x",
        "--exit-first",
        action="store_true",
        help="exit on first failure without deleting the clusters",
    )

    p = add_command(
        sp,
        "report",
        stress.report,
        help="generate markdown report from stress test results",
        envfile=False,
        description=options.dedent("""
            Generate a human-readable markdown report from the JSON
            results produced by a stress-test run.

            Examples:
              # Generate a report from stress test results
              drenv stress-test report out/test
            """),
    )
    p.add_argument(
        "directory",
        help="directory containing test.json",
    )

    p = add_command(
        sp,
        "compare",
        stress.compare,
        help="compare 2 stress tests",
        envfile=False,
        description=options.dedent("""
            Compare the results of two stress-test output directories and
            summarize the differences.

            Examples:
              # Compare two stress test results
              drenv stress-test compare out/before out/after
            """),
    )
    p.add_argument(
        "before",
        help="directory containing test.json (before)",
    )
    p.add_argument(
        "after",
        help="directory containing test.json (after)",
    )


def add_command(sp, name, func, help=None, envfile=True, description=None):
    parser = sp.add_parser(
        name,
        help=help,
        description=(
            options.wrap_description(description, width) if description else None
        ),
        formatter_class=options.formatter_class(width),
    )
    parser.add_argument(
        "--logfile",
        default=LOGFILE,
        help=f"path to logfile (default {LOGFILE!r})",
    )
    parser.set_defaults(func=func)
    if envfile:
        parser.add_argument(
            "--name-prefix",
            metavar="PREFIX",
            help="prefix profile names",
        )
        arg = parser.add_argument("envfile", help="path to environment file")
        arg.completer = argcomplete.completers.FilesCompleter(["*.yaml"])
    return parser


def load_env(args):
    with open(args.envfile) as f:
        return envfile.load(f, name_prefix=args.name_prefix)


def configure_logging(args):
    """
    Configure logging to log all message to the logfile, and INFO messages to
    the console.
    """
    formatter = logging.Formatter("%(asctime)s %(levelname)-7s %(message)s")

    logfile = logging.FileHandler(args.logfile)
    logfile.setFormatter(formatter)
    logfile.setLevel(logging.DEBUG)

    console = logging.StreamHandler(sys.stderr)
    console.setFormatter(formatter)
    console.setLevel(logging.INFO)

    logging.basicConfig(
        level=logging.DEBUG,
        handlers=[console, logfile],
    )


def shutdown_executors():
    """
    Shut down all executors and return True if any were shut down.

    When executors are used, the caller should also terminate the process
    group to clean up any child processes spawned by executor tasks.
    """
    # Prevents adding new executors, starting new child processes, and aborts
    # running workers.
    shutdown.start()

    # Cancels pending tasks and prevent submission of new tasks. This does not
    # affect running tasks, they will be aborted when they check shutdown
    # status, or when the child process terminates.
    for name, executor in executors:
        logging.debug("[main] Shutting down executor %s", name)
        executor.shutdown(wait=False, cancel_futures=True)

    return len(executors) > 0


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
    env = load_env(args)
    logging.info("[main] Setting up drenv")
    ssh.setup()
    for name in set(p["provider"] for p in env["profiles"]):
        logging.info("[main] Setting up '%s' for drenv", name)
        provider = providers.get(name)
        provider.setup()


def do_cleanup(args):
    env = load_env(args)
    logging.info("[main] Cleaning up drenv")
    for name in set(p["provider"] for p in env["profiles"]):
        logging.info("[main] Cleaning up '%s' for drenv", name)
        provider = providers.get(name)
        provider.cleanup()
    ssh.cleanup()


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


def do_gather(args):
    env = load_env(args)
    start = time.monotonic()
    logging.info("[%s] Gathering environment", env["name"])
    kubectl.gather(
        [p["name"] for p in env["profiles"]],
        directory=args.directory,
        namespaces=args.namespaces,
        name=env["name"],
    )
    logging.info(
        "[%s] Environment gathered in %.2f seconds",
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


def do_load(args):
    env = load_env(args)
    start = time.monotonic()
    logging.info("[%s] Loading image '%s'", env["name"], args.image)
    execute(load_image, env["profiles"], "profiles", image=args.image)
    logging.info(
        "[%s] Image loaded in %.2f seconds",
        env["name"],
        time.monotonic() - start,
    )


def do_suspend(args):
    env = load_env(args)
    logging.info("[%s] Suspending environment", env["name"])
    for profile in env["profiles"]:
        provider = providers.get(profile["provider"])
        provider.suspend(profile)


def do_resume(args):
    env = load_env(args)
    logging.info("[%s] Resuming environment", env["name"])
    for profile in env["profiles"]:
        provider = providers.get(profile["provider"])
        provider.resume(profile)


def do_dump(args):
    env = load_env(args)
    yaml.safe_dump(env, sys.stdout)


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
    provider = providers.get(profile["provider"])
    existing = provider.exists(profile)

    provider.start(
        profile,
        verbose=True,
        timeout=args.timeout,
        local_registry=args.local_registry,
    )
    provider.configure(profile, existing=existing, dns_mode=args.dns_mode)

    if existing:
        restart_failed_deployments(profile)

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

    if cluster_status != cluster.UNKNOWN:
        provider = providers.get(profile["provider"])
        provider.stop(profile)


def delete_cluster(profile, **options):
    provider = providers.get(profile["provider"])
    provider.delete(profile)

    profile_config = drenv.config_dir(profile["name"])
    if os.path.exists(profile_config):
        logging.info("[%s] Removing config %s", profile["name"], profile_config)
        shutil.rmtree(profile_config)


def load_image(profile, image=None, **options):
    provider = providers.get(profile["provider"])
    provider.load(profile, image)


def restart_failed_deployments(profile):
    """
    When restarting after failure, some deployment may enter failing state.
    This is not handled by the addons. Restarting the deployment solves this
    issue. This may also be solved at the addon level.
    """
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
        logging.info(
            "[%s] %s failed in %.2f seconds",
            name,
            hook,
            time.monotonic() - start,
        )
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
