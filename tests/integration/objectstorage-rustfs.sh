#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
PYTHON=${STELLARMESH_OBJECTSTORAGE_TEST_PYTHON:-python3}
# RustFS 1.0.0：固定官方多架构镜像摘要，仅用于隔离测试。
RUSTFS_IMAGE='rustfs/rustfs:1.0.0@sha256:8cc9801755448b71a786705ce76692c77e14936cccd87cf2fc31842e58f4d1ff'
RUN_ID="$$"
NETWORK="stellarmesh-objectstorage-test-${RUN_ID}"
RUSTFS_CONTAINER="stellarmesh-rustfs-${RUN_ID}"
TMP_DIR=$(mktemp -d)
ADMIN_USER='storageadmin'
ADMIN_PASSWORD='storage-admin-password-00000001'
PROJECT_USER='storageproject'
PROJECT_PASSWORD='storage-project-password-000001'
BUCKET='stellarmesh-storage-integration'

cleanup() {
    docker rm -f "$RUSTFS_CONTAINER" >/dev/null 2>&1 || true
    docker network rm "$NETWORK" >/dev/null 2>&1 || true
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

umask 077
cat >"$TMP_DIR/project-policy.json" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:GetBucketLocation", "s3:ListBucketMultipartUploads"],
      "Resource": ["arn:aws:s3:::$BUCKET"]
    },
    {
      "Effect": "Allow",
      "Action": ["s3:ListBucket"],
      "Resource": ["arn:aws:s3:::$BUCKET"]
    },
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:GetObjectVersion", "s3:PutObject", "s3:DeleteObject", "s3:DeleteObjectVersion", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"],
      "Resource": ["arn:aws:s3:::$BUCKET/integration/*"]
    }
  ]
}
EOF
chmod 0444 "$TMP_DIR/project-policy.json"

docker network create "$NETWORK" >/dev/null
docker run -d --name "$RUSTFS_CONTAINER" --network "$NETWORK" --network-alias rustfs \
    -p 127.0.0.1::9000 \
    -e "RUSTFS_ACCESS_KEY=$ADMIN_USER" \
    -e "RUSTFS_SECRET_KEY=$ADMIN_PASSWORD" \
    "$RUSTFS_IMAGE" >/dev/null
RUSTFS_PORT=$(docker port "$RUSTFS_CONTAINER" 9000/tcp | sed -n 's/.*://p' | head -n 1)
RUSTFS_PUBLIC="http://127.0.0.1:$RUSTFS_PORT"

attempt=0
until "$PYTHON" - "$RUSTFS_PUBLIC" <<'PY'
import sys
import urllib.error
import urllib.request

try:
    with urllib.request.urlopen(sys.argv[1] + "/health/ready", timeout=2) as response:
        raise SystemExit(0 if response.status == 200 else 1)
except (OSError, urllib.error.URLError):
    raise SystemExit(1)
PY
do
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 60 ]; then
        echo 'RustFS 未在预期时间内就绪' >&2
        exit 1
    fi
    sleep 1
done

"$ROOT/tests/integration/rustfs-setup.sh" "$TMP_DIR" "$RUSTFS_PUBLIC" "$BUCKET" \
    "$ADMIN_USER" "$ADMIN_PASSWORD" "$PROJECT_USER" "$PROJECT_PASSWORD"


export OBJECTSTORAGE_TEST_ENDPOINT="$RUSTFS_PUBLIC"
export OBJECTSTORAGE_TEST_BUCKET="$BUCKET"
export AWS_ACCESS_KEY_ID="$PROJECT_USER"
export AWS_SECRET_ACCESS_KEY="$PROJECT_PASSWORD"
export AWS_EC2_METADATA_DISABLED=true
"$PYTHON" -m pytest "$ROOT/sdk/python/objectstorage/tests/test_integration.py" -q
# 可选调用方验证入口，共享隔离 RustFS，不共享生产配置。
if [ "$#" -gt 0 ]; then
    "$@"
fi
