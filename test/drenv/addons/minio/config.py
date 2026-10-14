# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from pathlib import Path

PACKAGE_DIR = Path(__file__).parent

# Sync with minio.yaml
MINIO_ACCESS_KEY = "minio"
MINIO_SECRET_KEY = "minio123"

# Used by ramen and velero.
BUCKET = "bucket"
