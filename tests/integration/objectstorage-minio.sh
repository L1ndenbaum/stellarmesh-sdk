#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
PYTHON=${STELLARMESH_OBJECTSTORAGE_TEST_PYTHON:-python3}
# Quay 保留现有多架构 digest，切换镜像源不升级 MinIO 或 mc。
MINIO_IMAGE='quay.io/minio/minio@sha256:a1ea29fa28355559ef137d71fc570e508a214ec84ff8083e39bc5428980b015e'
MC_IMAGE='quay.io/minio/mc@sha256:aead63c77f9db9107f1696fb08ecb0faeda23729cde94b0f663edf4fe09728e3'
RUN_ID="$$"
NETWORK="stellarmesh-objectstorage-test-${RUN_ID}"
MINIO_CONTAINER="stellarmesh-minio-${RUN_ID}"
TMP_DIR=$(mktemp -d)
ADMIN_USER='storageadmin'
ADMIN_PASSWORD='storage-admin-password-00000001'
PROJECT_USER='storageproject'
PROJECT_PASSWORD='storage-project-password-000001'
BUCKET='stellarmesh-storage-integration'

cleanup() {
    docker rm -f "$MINIO_CONTAINER" >/dev/null 2>&1 || true
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
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"],
      "Resource": ["arn:aws:s3:::$BUCKET/integration/*"]
    }
  ]
}
EOF
chmod 0444 "$TMP_DIR/project-policy.json"

docker network create "$NETWORK" >/dev/null
docker run -d --name "$MINIO_CONTAINER" --network "$NETWORK" --network-alias minio \
    -p 127.0.0.1::9000 \
    -e "MINIO_ROOT_USER=$ADMIN_USER" \
    -e "MINIO_ROOT_PASSWORD=$ADMIN_PASSWORD" \
    "$MINIO_IMAGE" server /data >/dev/null
MINIO_PORT=$(docker port "$MINIO_CONTAINER" 9000/tcp | sed -n 's/.*://p' | head -n 1)
MINIO_PUBLIC="http://127.0.0.1:$MINIO_PORT"

attempt=0
until "$PYTHON" - "$MINIO_PUBLIC" <<'PY'
import sys
import urllib.error
import urllib.request

try:
    with urllib.request.urlopen(sys.argv[1] + "/minio/health/live", timeout=2) as response:
        raise SystemExit(0 if response.status == 200 else 1)
except (OSError, urllib.error.URLError):
    raise SystemExit(1)
PY
do
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 60 ]; then
        echo 'MinIO 未在预期时间内就绪' >&2
        exit 1
    fi
    sleep 1
done

docker run --rm --network "$NETWORK" --entrypoint /bin/sh \
    -v "$TMP_DIR/project-policy.json:/tmp/project-policy.json:ro" \
    "$MC_IMAGE" -ec "
        mc alias set admin http://minio:9000 '$ADMIN_USER' '$ADMIN_PASSWORD' >/dev/null
        mc mb admin/$BUCKET >/dev/null
        mc version enable admin/$BUCKET >/dev/null
        mc admin user add admin '$PROJECT_USER' '$PROJECT_PASSWORD' >/dev/null
        mc admin policy create admin storage-project /tmp/project-policy.json >/dev/null
        mc admin policy attach admin storage-project --user '$PROJECT_USER' >/dev/null
    "

if docker run --rm --network "$NETWORK" --entrypoint /bin/sh "$MC_IMAGE" -ec "
    mc alias set project http://minio:9000 '$PROJECT_USER' '$PROJECT_PASSWORD' >/dev/null
    mc mb project/forbidden-bucket
" >/dev/null 2>&1; then
    echo '项目凭据不应拥有 Bucket 创建权限' >&2
    exit 1
fi

export OBJECTSTORAGE_TEST_ENDPOINT="$MINIO_PUBLIC"
export OBJECTSTORAGE_TEST_BUCKET="$BUCKET"
export AWS_ACCESS_KEY_ID="$PROJECT_USER"
export AWS_SECRET_ACCESS_KEY="$PROJECT_PASSWORD"
export AWS_EC2_METADATA_DISABLED=true
"$PYTHON" -m pytest "$ROOT/sdk/python/objectstorage/tests/test_integration.py" -q
# 可选调用方验证入口，共享隔离 MinIO，不共享生产配置。
if [ "$#" -gt 0 ]; then
    "$@"
fi
