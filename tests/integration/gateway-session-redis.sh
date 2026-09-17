#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
# 真实 Redis 验证 Lua、过期和并发，固定版本与多架构 digest，不使用生产实例。
REDIS_IMAGE='redis:8.2.7-alpine@sha256:223b183cbc49f5ff48728e1fc52ccf101f05072decad2bd9867281a3c9bf75fd'
TMP_DIR=$(mktemp -d)
CONTAINER="stellarmesh-session-$(basename "$TMP_DIR")"
cleanup() {
    docker rm -fv "$CONTAINER" >/dev/null 2>&1 || true
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

docker run -d --name "$CONTAINER" -p 127.0.0.1::6379 \
    "$REDIS_IMAGE" redis-server --save '' --appendonly no >/dev/null
attempt=0
until docker exec "$CONTAINER" redis-cli ping >"$TMP_DIR/ping" 2>/dev/null && \
    test "$(cat "$TMP_DIR/ping")" = PONG
do
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 30 ]; then
        echo 'Session 测试 Redis 未就绪' >&2
        exit 1
    fi
    sleep 1
done
ADDRESS=$(docker port "$CONTAINER" 6379/tcp)
cd "$ROOT"
STELLARMESH_SESSION_REDIS_ADDR="$ADDRESS" go test -race -count=1 ./sdk/go/gateway/...
echo '网关 Redis Session 集成验证通过'
