# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import time

from drenv import kubectl
from drenv import mc
from drenv import minio

from .config import BUCKET, MINIO_ACCESS_KEY, MINIO_SECRET_KEY, PACKAGE_DIR


def start(cluster):
    """
    Deploy minio, set up mc alias, and create the bucket.
    """
    deploy(cluster)
    wait(cluster)


def deploy(cluster):
    print("Deploying minio")
    kubectl.apply(
        "--filename", str(PACKAGE_DIR / "start-data" / "minio.yaml"), context=cluster
    )


def wait(cluster):
    print("Waiting until minio is rolled out")
    kubectl.rollout(
        "status",
        "deployment/minio",
        "--namespace=minio",
        context=cluster,
    )

    attempts = 5
    delay = 1
    start_time = time.monotonic()

    for i in range(1, attempts + 1):
        print(f"Waiting until minio service is available (attempt {i}/{attempts})")
        minio.wait_for_service(cluster)

        print(f"Setting mc alias {cluster} (attempt {i}/{attempts})")
        try:
            mc.set_alias(
                cluster,
                minio.service_url(cluster),
                MINIO_ACCESS_KEY,
                MINIO_SECRET_KEY,
            )
        except Exception as e:
            if i == attempts:
                raise

            print(f"Attempt {i} failed: {e}")
            print(f"Retrying in {delay} seconds")
            time.sleep(delay)
            delay *= 2
        else:
            elapsed = time.monotonic() - start_time
            print(f"Set mc alias in {elapsed:.2f} seconds")
            break

    print(f"Creating bucket '{cluster}/{BUCKET}'")
    mc.mb(f"{cluster}/{BUCKET}", ignore_existing=True)
