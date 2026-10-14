# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import clusteradm
from drenv import kubectl

# Use default image version.
BUNDLE_VERSION = None

# Use default image registry.
IMAGE_REGISTRY = None

ADDONS = (
    {
        "name": "application-manager",
        "version": BUNDLE_VERSION,
    },
    {
        "name": "governance-policy-framework",
        "version": BUNDLE_VERSION,
    },
)

DEPLOYMENTS = {
    "open-cluster-management": (
        "cluster-manager",
        "governance-policy-addon-controller",
        "governance-policy-propagator",
        "multicluster-operators-appsub-summary",
        "multicluster-operators-channel",
        "multicluster-operators-placementrule",
        "multicluster-operators-subscription",
    ),
    "open-cluster-management-hub": (
        "cluster-manager-placement-controller",
        "cluster-manager-registration-controller",
        "cluster-manager-registration-webhook",
        "cluster-manager-work-webhook",
    ),
}


def start(cluster):
    deploy(cluster)
    wait(cluster)


def deploy(cluster):
    print("Initializing hub")
    clusteradm.init(
        # With auto approval joined clusters are accepted automatically.
        feature_gates=["ManagedClusterAutoApproval=true"],
        bundle_version=BUNDLE_VERSION,
        image_registry=IMAGE_REGISTRY,
        wait=True,
        context=cluster,
    )

    print("Installing hub addons")
    for addon in ADDONS:
        clusteradm.install(
            "hub-addon",
            names=[addon["name"]],
            bundle_version=addon["version"],
            context=cluster,
        )


def wait(cluster):
    print("Waiting until deployments are rolled out")
    for ns, names in DEPLOYMENTS.items():
        for name in names:
            deployment = f"deploy/{name}"
            print(f"Waiting until deployment '{ns}/{name}' exists")
            kubectl.wait(
                deployment, "--for=create", f"--namespace={ns}", context=cluster
            )
            print(f"Waiting until deployment '{ns}/{name}' is rolled out")
            kubectl.rollout(
                "status",
                deployment,
                f"--namespace={ns}",
                context=cluster,
            )
