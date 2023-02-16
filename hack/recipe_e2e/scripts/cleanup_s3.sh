#!/bin/bash
S3_STORE_ALIAS=${1:-minio-cluster1}
NAMESPACE=${2:-recipe-test}
BUCKET=${3:-velero}

echo "cleanup_s3.sh: S3_STORE_ALIAS: '$S3_STORE_ALIAS' NAMESPACE: '$NAMESPACE' BUCKET: '$BUCKET'"
S3_PATH=$S3_STORE_ALIAS/$BUCKET/$NAMESPACE

if [[ $(mc ls "$S3_PATH" | wc -l) -gt 0 ]]; then
    echo "removing all contents from $S3_PATH"
    mc rm -r --force "$S3_PATH"
fi
