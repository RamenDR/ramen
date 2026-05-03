# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import cache as _cache
from drenv import cluster as _cluster
from drenv import commands
from drenv import kubectl
from drenv import temporary_kubeconfig

from .config import CACHE_KEY, PACKAGE_DIR


def start(hub, *clusters):
    """
    Deploy argocd on the hub and add clusters.
    """
    wait_for_clusters(clusters)
    deploy_argocd(hub)
    wait_for_deployments(hub)
    add_clusters(hub, clusters)


def wait_for_clusters(clusters):
    for name in clusters:
        print(f"Waiting until cluster '{name}' is ready")
        _cluster.wait_until_ready(name)


def deploy_argocd(cluster):
    print("Deploying argocd")
    path = _cache.get(str(PACKAGE_DIR / "start-data"), CACHE_KEY)
    kubectl.apply("--filename", path, "--namespace", "argocd", context=cluster)


def wait_for_deployments(cluster):
    print("Waiting until all deployments are available")
    kubectl.wait(
        "deploy",
        "--all",
        "--for=condition=Available",
        "--namespace=argocd",
        context=cluster,
    )


def add_clusters(hub, clusters):
    # Need to use KUBECONFIG env, switch to hub cluster argocd ns first,
    # otherwise will hit argocd command bug.
    # See https://github.com/argoproj/argo-cd/issues/14167
    with temporary_kubeconfig("drenv-argocd-test.") as env:
        kubeconfig = env["KUBECONFIG"]
        kubectl.config("use-context", hub, "--kubeconfig", kubeconfig)
        kubectl.config(
            "set-context",
            "--current",
            "--namespace=argocd",
            f"--kubeconfig={kubeconfig}",
        )

        print("Logging in to argocd server on hub")
        for line in commands.watch("argocd", "login", "--core", env=env):
            print(line)

        for name in clusters:
            try:
                print(f"Adding cluster '{name}' to argocd")
                commands.run("argocd", "cluster", "add", name, "-y", env=env)
            except commands.Error as e:
                # Ignore known error "NOAUTH" with "argocd cluster add"
                # after "argocd login --core".
                # See https://github.com/argoproj/argo-cd/issues/18464
                if e.exitcode != 20 or "NOAUTH" not in e.error:
                    raise e
