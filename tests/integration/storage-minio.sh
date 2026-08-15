#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
MINIO_IMAGE='minio/minio@sha256:a1ea29fa28355559ef137d71fc570e508a214ec84ff8083e39bc5428980b015e'
MC_IMAGE='minio/mc@sha256:aead63c77f9db9107f1696fb08ecb0faeda23729cde94b0f663edf4fe09728e3'
RUN_ID="$$"
NETWORK="stellarmesh-storage-test-${RUN_ID}"
MINIO_CONTAINER="stellarmesh-minio-${RUN_ID}"
SERVICE_CONTAINER="stellarmesh-storage-${RUN_ID}"
TMP_DIR=$(mktemp -d)
ADMIN_USER='storageadmin'
ADMIN_PASSWORD='storage-admin-password-00000001'
PROJECT_USER='storageproject'
PROJECT_PASSWORD='storage-project-password-000001'
SERVICE_TOKEN='storage-service-integration-token-000001'
READER_TOKEN='storage-service-reader-token-000000002'
BUCKET='stellarmesh-storage-integration'

cleanup() {
    docker rm -f "$SERVICE_CONTAINER" >/dev/null 2>&1 || true
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
cat >"$TMP_DIR/access.json" <<EOF
{
  "namespaces": {
    "documents": {"bucket": "$BUCKET", "prefix": "integration/"}
  },
  "principals": {
    "backend": {
      "tokens": ["$SERVICE_TOKEN"],
      "grants": {"documents": ["read", "write", "delete"]}
    },
    "reader": {
      "tokens": ["$READER_TOKEN"],
      "grants": {"documents": ["read"]}
    }
  }
}
EOF
chmod 0444 "$TMP_DIR/access.json" "$TMP_DIR/project-policy.json"

docker network create "$NETWORK" >/dev/null
docker run -d --name "$MINIO_CONTAINER" --network "$NETWORK" --network-alias minio \
    -p 127.0.0.1::9000 \
    -e "MINIO_ROOT_USER=$ADMIN_USER" \
    -e "MINIO_ROOT_PASSWORD=$ADMIN_PASSWORD" \
    "$MINIO_IMAGE" server /data >/dev/null
MINIO_PORT=$(docker port "$MINIO_CONTAINER" 9000/tcp | sed -n 's/.*://p' | head -n 1)
MINIO_PUBLIC="http://127.0.0.1:$MINIO_PORT"

attempt=0
until python3 - "$MINIO_PUBLIC" <<'PY'
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

docker run -d --name "$SERVICE_CONTAINER" --network "$NETWORK" \
    -p 127.0.0.1::8090 \
    -v "$TMP_DIR/access.json:/run/secrets/storage-access.json:ro" \
    -e STELLARMESH_STORAGE_ACCESS_FILE=/run/secrets/storage-access.json \
    -e STELLARMESH_STORAGE_ENDPOINT=http://minio:9000 \
    -e "STELLARMESH_STORAGE_PRESIGN_ENDPOINT=$MINIO_PUBLIC" \
    -e STELLARMESH_STORAGE_USE_PATH_STYLE=true \
    -e STELLARMESH_STORAGE_S3_CHECK_TIMEOUT=1s \
    -e STELLARMESH_STORAGE_S3_CHECK_INTERVAL=1s \
    -e AWS_REGION=us-east-1 \
    -e AWS_EC2_METADATA_DISABLED=true \
    -e "AWS_ACCESS_KEY_ID=$PROJECT_USER" \
    -e "AWS_SECRET_ACCESS_KEY=$PROJECT_PASSWORD" \
    stellarmesh-storage-service:test >/dev/null
SERVICE_PORT=$(docker port "$SERVICE_CONTAINER" 8090/tcp | sed -n 's/.*://p' | head -n 1)
SERVICE_URL="http://127.0.0.1:$SERVICE_PORT"

wait_ready() {
    expected=$1
    count=0
    while [ "$count" -lt 60 ]; do
        status=$(python3 - "$SERVICE_URL" <<'PY'
import sys
import urllib.error
import urllib.request

try:
    with urllib.request.urlopen(sys.argv[1] + "/health/ready", timeout=2) as response:
        print(response.status)
except urllib.error.HTTPError as error:
    print(error.code)
except OSError:
    print(0)
PY
)
        if [ "$status" = "$expected" ]; then
            return 0
        fi
        count=$((count + 1))
        sleep 1
    done
    echo "storage-service readiness 未切换到 $expected" >&2
    docker logs "$SERVICE_CONTAINER" >&2 || true
    return 1
}

wait_ready 200
python3 "$ROOT/tests/integration/storage-pipeline.py" \
    --base-url "$SERVICE_URL" --token "$SERVICE_TOKEN" --reader-token "$READER_TOKEN"

STELLARMESH_STORAGE_MINIO_INTEGRATION=1 \
STELLARMESH_STORAGE_MINIO_ENDPOINT="$MINIO_PUBLIC" \
STELLARMESH_STORAGE_MINIO_BUCKET="$BUCKET" \
STELLARMESH_STORAGE_MINIO_PREFIX='integration/go-range/' \
AWS_REGION=us-east-1 \
AWS_ACCESS_KEY_ID="$PROJECT_USER" \
AWS_SECRET_ACCESS_KEY="$PROJECT_PASSWORD" \
go test ./sdk/go/objectstorage/s3store -run '^TestMinIOIntegrationRange$' -count=1

docker stop "$MINIO_CONTAINER" >/dev/null
wait_ready 503
docker start "$MINIO_CONTAINER" >/dev/null
wait_ready 200

docker stop --time 10 "$SERVICE_CONTAINER" >/dev/null
SERVICE_EXIT_CODE=$(docker inspect -f '{{.State.ExitCode}}' "$SERVICE_CONTAINER")
if [ "$SERVICE_EXIT_CODE" != "0" ]; then
    echo "storage-service 优雅关闭退出码为 $SERVICE_EXIT_CODE" >&2
    docker logs "$SERVICE_CONTAINER" >&2 || true
    exit 1
fi

echo 'Storage MinIO 集成验证通过'
