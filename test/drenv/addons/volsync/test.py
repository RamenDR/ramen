# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import concurrent.futures
import json
from pathlib import Path

import yaml

import drenv
from drenv import kubectl
from drenv import subctl

PACKAGE_DIR = Path(__file__).parent

DEPLOY = "busybox"
NAMESPACE = "volsync-test"

VOLSYNC_SECRET = "volsync-rsync-tls-busybox-dst"
VOLSYNC_SERVICE = "volsync-rsync-tls-dst-busybox-dst"

_TEST_DATA = str(PACKAGE_DIR / "test-data")


def test(cluster1, cluster2):
    with concurrent.futures.ThreadPoolExecutor() as e:
        tests = [
            e.submit(test_variant, cluster1, cluster2, "file"),
            e.submit(test_variant, cluster1, cluster2, "block"),
        ]
        for t in concurrent.futures.as_completed(tests):
            t.result()


def test_variant(cluster1, cluster2, variant):
    setup_application(cluster1, variant)
    setup_replication_destination(cluster2, variant)

    wait_for_application(cluster1, variant)
    wait_for_replication_destination(cluster2, variant)

    setup_replication_secret(cluster1, cluster2, variant)
    setup_replication_service(cluster1, cluster2, variant)

    run_replication(cluster1, variant)
    teardown(cluster1, cluster2, variant)


def setup_application(cluster, variant):
    print(
        f"Deploy application in namesapce '{NAMESPACE}-{variant}' cluster '{cluster}'"
    )
    kubectl.apply("--kustomize", f"{_TEST_DATA}/app/{variant}", context=cluster)


def setup_replication_destination(cluster, variant):
    print(
        f"Create replication destination in namespace '{NAMESPACE}-{variant}' cluster '{cluster}'"
    )
    kubectl.apply("--kustomize", f"{_TEST_DATA}/rd/{variant}", context=cluster)


def wait_for_application(cluster, variant):
    print(
        f"Waiting until deploy '{DEPLOY}' is rolled out in namespace '{NAMESPACE}-{variant}' "
        f"cluster '{cluster}'"
    )
    kubectl.rollout(
        "status",
        f"deploy/{DEPLOY}",
        f"--namespace={NAMESPACE}-{variant}",
        timeout=120,
        context=cluster,
    )


def wait_for_replication_destination(cluster, variant):
    print(
        f"Waiting until replication destination is synchronizing in namespace "
        f"'{NAMESPACE}-{variant}' cluster '{cluster}'"
    )
    kubectl.wait(
        "replicationdestination/busybox-dst",
        "--for=condition=Synchronizing=True",
        f"--namespace={NAMESPACE}-{variant}",
        timeout=120,
        context=cluster,
    )


def setup_replication_secret(cluster1, cluster2, variant):
    """
    Create a secret in the source cluster using data from the secret created by
    volsync on the destiantion cluster.
    """
    print(
        f"Getting volsync secret in namespace '{NAMESPACE}-{variant}' cluster '{cluster2}'"
    )
    psk_txt = kubectl.get(
        f"secret/{VOLSYNC_SECRET}",
        f"--namespace={NAMESPACE}-{variant}",
        "--output=jsonpath={.data.psk\\.txt}",
        context=cluster2,
    )

    print(
        f"Creating volsync secret in namespace '{NAMESPACE}-{variant}' cluster '{cluster1}'"
    )
    template = drenv.template(f"{_TEST_DATA}/rs/{variant}/secret.yaml")
    secret_yaml = template.substitute(value=psk_txt)
    kubectl.apply(
        "--filename=-",
        f"--namespace={NAMESPACE}-{variant}",
        input=secret_yaml,
        context=cluster1,
    )


def setup_replication_service(cluster1, cluster2, variant):
    """
    Export volsync replication service from the destination cluster to the
    source cluster using submariner.
    """
    print(
        f"Exporting volsync service in namespace '{NAMESPACE}-{variant}' cluster '{cluster2}'"
    )
    subctl.export(
        "service", VOLSYNC_SERVICE, cluster2, namespace=f"{NAMESPACE}-{variant}"
    )

    print(
        f"Waiting until service export is synced in namespace '{NAMESPACE}-{variant}' cluster '{cluster2}'"
    )
    kubectl.wait(
        f"serviceexports/{VOLSYNC_SERVICE}",
        "--for=condition=Ready",
        f"--namespace={NAMESPACE}-{variant}",
        timeout=120,
        context=cluster2,
    )

    print(
        f"Waiting until serviceimport '{NAMESPACE}-{variant}/{VOLSYNC_SERVICE}' exists in cluster '{cluster1}'"
    )
    kubectl.wait(
        f"serviceimports/{VOLSYNC_SERVICE}",
        "--for=create",
        f"--namespace={NAMESPACE}-{variant}",
        timeout=120,
        context=cluster1,
    )

    print(
        f"Waiting until serviceimport '{NAMESPACE}-{variant}/{VOLSYNC_SERVICE}' has clusters in cluster '{cluster1}'"
    )
    kubectl.wait(
        f"serviceimports/{VOLSYNC_SERVICE}",
        "--for=jsonpath={.status.clusters}",
        f"--namespace={NAMESPACE}-{variant}",
        timeout=120,
        context=cluster1,
    )


def run_replication(cluster, variant):
    """
    Start replication and wait until replication completes.
    """
    print(
        f"Creating replication source in namespace '{NAMESPACE}-{variant}' cluster '{cluster}'"
    )
    kubectl.apply(
        "--filename",
        f"{_TEST_DATA}/rs/{variant}/rs.yaml",
        f"--namespace={NAMESPACE}-{variant}",
        context=cluster,
    )

    print(
        f"Waiting until replicationsource '{NAMESPACE}-{variant}/busybox-src' reports sync status in cluster '{cluster}'"
    )
    kubectl.wait(
        "replicationsource/busybox-src",
        "--for=jsonpath={.status.lastManualSync}",
        f"--namespace={NAMESPACE}-{variant}",
        timeout=120,
        context=cluster,
    )

    print(
        f"Waiting until replication is completed in namespace '{NAMESPACE}-{variant}' cluster '{cluster}'"
    )
    kubectl.wait(
        "replicationsource/busybox-src",
        "--for=jsonpath={.status.lastManualSync}=replication-1",
        f"--namespace={NAMESPACE}-{variant}",
        timeout=120,
        context=cluster,
    )
    out = kubectl.get(
        "replicationsource/busybox-src",
        "--output=jsonpath={.status}",
        f"--namespace={NAMESPACE}-{variant}",
        context=cluster,
    )
    status = json.loads(out)
    print("Replication status:")
    print(yaml.dump(status))


def teardown(cluster1, cluster2, variant):
    """
    Remove deployments from both clusters. This also deletes additonal
    resources created in the same namespace.
    """
    print(
        f"Delete replication source in namespace '{NAMESPACE}-{variant}' cluster '{cluster1}'"
    )
    kubectl.delete(
        "--filename",
        f"{_TEST_DATA}/rs/{variant}/rs.yaml",
        f"--namespace={NAMESPACE}-{variant}",
        context=cluster1,
    )

    print(
        f"Unexporting volsync service in namespace '{NAMESPACE}-{variant}' cluster '{cluster2}'"
    )
    subctl.unexport(
        "service", VOLSYNC_SERVICE, cluster2, namespace=f"{NAMESPACE}-{variant}"
    )

    print(
        f"Delete application in namespace '{NAMESPACE}-{variant}' cluster '{cluster1}'"
    )
    kubectl.delete(
        "--kustomize",
        f"{_TEST_DATA}/app/{variant}",
        "--ignore-not-found",
        "--wait=false",
        context=cluster1,
    )

    print(
        f"Delete replication destination in namespace '{NAMESPACE}-{variant}' cluster '{cluster2}'"
    )
    kubectl.delete(
        "--kustomize",
        f"{_TEST_DATA}/rd/{variant}",
        "--ignore-not-found",
        "--wait=false",
        context=cluster2,
    )

    for cluster in cluster1, cluster2:
        print(
            f"Waiting until namespace '{NAMESPACE}-{variant}' is deleted in cluster '{cluster}'"
        )
        kubectl.wait(
            "ns",
            f"{NAMESPACE}-{variant}",
            "--for=delete",
            timeout=120,
            context=cluster,
        )
