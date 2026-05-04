# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from pathlib import Path

from drenv import commands
from drenv import minio

PACKAGE_DIR = Path(__file__).parent


def start(cluster):
    print("Deploying velero")
    s3_url = minio.service_url(cluster)
    credentials = str(PACKAGE_DIR / "start-data" / "credentials.conf")
    for line in commands.watch(
        "velero",
        "install",
        "--provider=aws",
        "--image=quay.io/prd/velero:v1.16.1",
        "--plugins=quay.io/prd/velero-plugin-for-aws:v1.12.0,quay.io/kubevirt/kubevirt-velero-plugin:v0.8.0",
        "--bucket=bucket",
        f"--secret-file={credentials}",
        "--use-volume-snapshots=false",
        f"--backup-location-config=region=minio,s3ForcePathStyle=true,s3Url={s3_url}",
        f"--kubecontext={cluster}",
        "--wait",
    ):
        print(line)
