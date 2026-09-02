# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from pathlib import Path

from drenv import cache as _cache
from drenv import kubectl

PACKAGE_DIR = Path(__file__).parent
DEPS_DIR = PACKAGE_DIR / "start-data" / "deps"
OPERATOR_DIR = PACKAGE_DIR / "start-data" / "operator"
DEPS_CACHE_KEY = "addons/rook-operator-deps-1.20-1.yaml"
OPERATOR_CACHE_KEY = "addons/rook-operator-1.20-2.yaml"

# operator.yaml includes OperatorConfig and Driver CRs. Those CRDs are in
# csi-operator.yaml, so they must be established before the second apply.
CSI_CRDS = (
    "operatorconfigs.csi.ceph.io",
    "drivers.csi.ceph.io",
)


def start(cluster):
    """
    Deploy the rook ceph operator and wait until it is ready.
    """
    deploy_deps(cluster)
    deploy_operator(cluster)


def cache():
    """
    Refresh the cached kustomization yaml.
    """
    _cache.refresh(str(DEPS_DIR), DEPS_CACHE_KEY)
    _cache.refresh(str(OPERATOR_DIR), OPERATOR_CACHE_KEY)


def deploy_deps(cluster):
    print("Deploying rook operator dependencies")
    path = _cache.get(str(DEPS_DIR), DEPS_CACHE_KEY)
    kubectl.apply("--filename", path, context=cluster)

    print("Waiting until CSI operator CRDs are established")
    for crd in CSI_CRDS:
        kubectl.wait(
            f"crd/{crd}",
            "--for=condition=established",
            context=cluster,
        )

    print("Waiting until ceph-csi operator is rolled out")
    kubectl.rollout(
        "status",
        "deploy/ceph-csi-controller-manager",
        "--namespace=rook-ceph",
        context=cluster,
    )


def deploy_operator(cluster):
    print("Deploying rook ceph operator")
    path = _cache.get(str(OPERATOR_DIR), OPERATOR_CACHE_KEY)
    kubectl.apply("--filename", path, context=cluster)

    print("Waiting until rook ceph operator is rolled out")
    kubectl.rollout(
        "status",
        "deploy/rook-ceph-operator",
        "--namespace=rook-ceph",
        # We had random timeout with 300s.
        timeout=600,
        context=cluster,
    )

    print("Waiting until rook ceph operator is ready")
    kubectl.wait(
        "pod",
        "--for=jsonpath={.status.phase}=Running",
        "--namespace=rook-ceph",
        "--selector=app=rook-ceph-operator",
        context=cluster,
    )
