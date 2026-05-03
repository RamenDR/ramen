# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from pathlib import Path

from drenv import kubectl

PACKAGE_DIR = Path(__file__).parent

_PVC_DATA = str(PACKAGE_DIR / "test-data" / "pvc")
_DV_DATA = str(PACKAGE_DIR / "test-data" / "dv")


def test(cluster):
    """
    Test CDI by creating a PVC and DataVolume and waiting until they are bound.
    """
    print("Deploying disk")
    kubectl.apply(f"--kustomize={_PVC_DATA}", context=cluster)

    print("Deploying data volume")
    kubectl.apply(f"--kustomize={_DV_DATA}", context=cluster)

    print("Waiting until pvc test-pvc is bound")
    kubectl.wait(
        "pvc/test-pvc",
        "--for=jsonpath={.status.phase}=Bound",
        timeout=120,
        context=cluster,
    )

    print("Waiting until data volume test-dv is bound")
    kubectl.wait(
        "dv/test-dv",
        "--for=condition=Bound",
        timeout=120,
        context=cluster,
    )

    print("Waiting until pvc test-dv is bound")
    kubectl.wait(
        "pvc/test-dv",
        "--for=jsonpath={.status.phase}=Bound",
        timeout=120,
        context=cluster,
    )

    print("Deletting pvc test-pvc")
    kubectl.delete(f"--kustomize={_PVC_DATA}", context=cluster)

    print("Deletting data volume test-dv")
    kubectl.delete(f"--kustomize={_DV_DATA}", context=cluster)
