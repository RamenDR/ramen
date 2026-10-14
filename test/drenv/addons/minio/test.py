# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import hashlib

from drenv import mcli

from .config import BUCKET, PACKAGE_DIR

# Use minio.yaml as convenient test data for S3 operations.
_TEST_FILE = PACKAGE_DIR / "start-data" / "minio.yaml"


def test(cluster):
    """
    Test minio by copying a file to/from S3 and verifying checksums.
    """
    with open(_TEST_FILE, "rb") as f:
        c1 = hashlib.sha256(f.read()).hexdigest()

    target = f"{cluster}/{BUCKET}/drenv/addons/minio/test"

    print(f"Copying minio.yaml to {target}")
    mcli.cp(str(_TEST_FILE), target)

    print("cluster s3 store contents:")
    print(mcli.ls(cluster, recursive=True))

    print(f"Getting {target} data")
    data = mcli.cat(target)

    print(f"Removing {target}")
    mcli.rm(target)

    print("cluster s3 store contents:")
    print(mcli.ls(cluster, recursive=True))

    print("Comparing checksums")
    c2 = hashlib.sha256(data).hexdigest()
    if c1 != c2:
        raise RuntimeError(f"Checksum mismatch: {c1} != {c2}")
