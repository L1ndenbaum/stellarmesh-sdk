#!/bin/sh
set -eu

clickhouse_image='clickhouse/clickhouse-server@sha256:f40cd6034fb8c54dce6a85338750fbad79f387e2705e1991a85f2e7086b5b9ea'
redpanda_image='redpandadata/redpanda@sha256:8f7e9e4c1422baaa1a5e2a6c6c668cfe05442cb3cb476542c7dff61725e6fe31'
run_id="$$"
network="stellarmesh-it-$run_id"
clickhouse="stellarmesh-it-clickhouse-$run_id"
kafka="stellarmesh-it-kafka-$run_id"
sink="stellarmesh-it-sink-$run_id"
ingester="stellarmesh-it-ingester-$run_id"
source_topic='stellarmesh.logging.events.v2'
dlq_topic='stellarmesh.logging.events.v2.dlq'
event_id='11111111-1111-4111-8111-111111111111'
spooled_event_id='22222222-2222-4222-8222-222222222222'
audit_event_id='33333333-3333-4333-8333-333333333333'
spooled_audit_event_id='44444444-4444-4444-8444-444444444444'
historical_log_id='55555555-5555-4555-8555-555555555555'
historical_audit_id='66666666-6666-4666-8666-666666666666'
invalid_migration_id='77777777-7777-4777-8777-777777777777'
service_token="$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')"
auth_file="$(mktemp)"

cleanup() {
    status="$?"
    trap - EXIT
    if [ "$status" -ne 0 ]; then
        docker logs "$ingester" 2>/dev/null || true
        docker logs "$sink" 2>/dev/null || true
        docker logs "$kafka" 2>/dev/null || true
        docker logs "$clickhouse" 2>/dev/null || true
    fi
    docker rm -f "$ingester" "$sink" "$kafka" "$clickhouse" >/dev/null 2>&1 || true
    docker network rm "$network" >/dev/null 2>&1 || true
    rm -f "$auth_file"
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

wait_until() {
    label="$1"
    shift
    attempts=60
    while ! "$@" >/dev/null 2>&1; do
        attempts=$((attempts - 1))
        if [ "$attempts" -le 0 ]; then
            echo "等待超时: $label" >&2
            return 1
        fi
        sleep 1
    done
}

clickhouse_has_event() {
    result="$(docker exec "$clickhouse" clickhouse-client --database logging --query "SELECT kind, level FROM log_events WHERE event_id = '$event_id' FORMAT TSV")"
    [ "$result" = "LOG	INFO" ]
}

clickhouse_has_spooled_event() {
    result="$(docker exec "$clickhouse" clickhouse-client --database logging --query "SELECT kind, level FROM log_events WHERE event_id = '$spooled_event_id' FORMAT TSV")"
    [ "$result" = "LOG	ERROR" ]
}

clickhouse_has_audit_event() {
    result="$(docker exec "$clickhouse" clickhouse-client --database logging --query "SELECT kind, level FROM log_events WHERE event_id = '$audit_event_id' FORMAT TSV")"
    [ "$result" = "AUDIT	INFO" ]
}

clickhouse_has_spooled_audit_event() {
    result="$(docker exec "$clickhouse" clickhouse-client --database logging --query "SELECT kind, level FROM log_events WHERE event_id = '$spooled_audit_event_id' FORMAT TSV")"
    [ "$result" = "AUDIT	INFO" ]
}

spool_has_segments() {
    docker exec "$ingester" find /var/lib/stellarmesh-logging/spool/batches -type f -name '*.ready.jsonl' | grep -q .
}

spool_is_empty() {
    ! spool_has_segments
}

sink_is_ready() {
    docker exec "$sink" wget -qO- http://127.0.0.1:8092/health/ready | grep -q '"status":"ready"'
}

migrate_logging() {
    docker run --rm --network "$network" stellarmesh-logging-clickhouse-migrate:test \
        -database 'clickhouse://clickhouse:9000?username=default&database=logging&x-multi-statement=true' "$@"
}

docker network create "$network" >/dev/null
docker run -d --name "$clickhouse" --hostname clickhouse --network "$network" \
    -e CLICKHOUSE_DB=logging \
    -e CLICKHOUSE_SKIP_USER_SETUP=1 \
    "$clickhouse_image" >/dev/null
docker run -d --name "$kafka" --hostname kafka --network "$network" \
    "$redpanda_image" redpanda start \
    --mode dev-container --smp 1 --memory 512M --reserve-memory 0M --node-id 0 \
    --kafka-addr internal://0.0.0.0:9092 \
    --advertise-kafka-addr internal://kafka:9092 >/dev/null

wait_until 'ClickHouse 就绪' docker exec "$clickhouse" clickhouse-client --query 'SELECT 1'
wait_until 'ClickHouse 容器网络就绪' docker run --rm --network "$network" "$clickhouse_image" \
    clickhouse-client --host clickhouse --query 'SELECT 1'
wait_until 'Kafka 就绪' docker exec "$kafka" rpk cluster health --exit-when-healthy
docker exec "$kafka" rpk topic create "$source_topic" "$dlq_topic" >/dev/null

migrate_logging up 1
docker exec "$clickhouse" clickhouse-client --database logging --query \
    "INSERT INTO log_events VALUES ('$invalid_migration_id', '2026-08-01 11:59:57.000', 'NOTICE', 'integration-service', 'invalid historical level', '', '{}', '2026-08-01 11:59:57.000')"
if migrate_logging up 1; then
    echo '未知历史 level 未阻止迁移' >&2
    exit 1
fi
kind_columns="$(docker exec "$clickhouse" clickhouse-client --query \
    "SELECT count() FROM system.columns WHERE database = 'logging' AND table = 'log_events' AND name = 'kind'")"
[ "$kind_columns" = '0' ]
migrate_logging force 1
docker exec "$clickhouse" clickhouse-client --database logging --query \
    "ALTER TABLE log_events DELETE WHERE event_id = '$invalid_migration_id' SETTINGS mutations_sync = 2"
docker exec "$clickhouse" clickhouse-client --database logging --multiquery --query \
    "INSERT INTO log_events VALUES ('$historical_log_id', '2026-08-01 11:59:58.000', 'INFO', 'integration-service', 'historical log', '', '{}', '2026-08-01 11:59:58.000');
     INSERT INTO log_events VALUES ('$historical_audit_id', '2026-08-01 11:59:59.000', 'AUDIT', 'integration-service', 'historical audit', '', '{}', '2026-08-01 11:59:59.000');"
migrate_logging up 1
historical_rows="$(docker exec "$clickhouse" clickhouse-client --database logging --query \
    "SELECT kind, level FROM log_events WHERE event_id IN ('$historical_log_id', '$historical_audit_id') ORDER BY event_id FORMAT TSV")"
[ "$historical_rows" = "LOG	INFO
AUDIT	INFO" ]

docker exec "$clickhouse" clickhouse-client --database logging --query \
    "INSERT INTO log_events (event_id, timestamp, kind, level, service, message, trace_id, metadata_json, ingested_at) VALUES ('$invalid_migration_id', '2026-08-01 11:59:57.000', 'SECURITY', 'INFO', 'integration-service', 'invalid historical kind', '', '{}', '2026-08-01 11:59:57.000')"
if migrate_logging down 1; then
    echo '未知历史 kind 未阻止降级' >&2
    exit 1
fi
migrate_logging force 2
docker exec "$clickhouse" clickhouse-client --database logging --query \
    "ALTER TABLE log_events DELETE WHERE event_id = '$invalid_migration_id' SETTINGS mutations_sync = 2"
migrate_logging down 1
historical_levels="$(docker exec "$clickhouse" clickhouse-client --database logging --query \
    "SELECT level FROM log_events WHERE event_id IN ('$historical_log_id', '$historical_audit_id') ORDER BY event_id FORMAT TSV")"
[ "$historical_levels" = "INFO
AUDIT" ]
migrate_logging up 1
historical_rows="$(docker exec "$clickhouse" clickhouse-client --database logging --query \
    "SELECT kind, level FROM log_events WHERE event_id IN ('$historical_log_id', '$historical_audit_id') ORDER BY event_id FORMAT TSV")"
[ "$historical_rows" = "LOG	INFO
AUDIT	INFO" ]

docker run -d --name "$sink" --network "$network" \
    -e STELLARMESH_LOGGING_KAFKA_BROKERS=kafka:9092 \
    -e STELLARMESH_LOGGING_KAFKA_TOPIC="$source_topic" \
    -e STELLARMESH_LOGGING_KAFKA_DLQ_TOPIC="$dlq_topic" \
    -e STELLARMESH_LOGGING_CLICKHOUSE_HTTP_URL=http://clickhouse:8123 \
    -e STELLARMESH_LOGGING_CLICKHOUSE_DATABASE=logging \
    -e STELLARMESH_LOGGING_CLICKHOUSE_USER=default \
    -e STELLARMESH_LOGGING_WRITER_MAX_SOURCE_MESSAGE_BYTES=512 \
    stellarmesh-logging-clickhouse-sink:test >/dev/null
wait_until 'ClickHouse sink 就绪' sink_is_ready

printf '{"services":{"integration-service":["%s"]}}\n' "$service_token" >"$auth_file"
chmod 444 "$auth_file"
docker run -d --name "$ingester" --network "$network" -p 127.0.0.1:0:8091 \
    --mount "type=bind,src=$auth_file,dst=/run/secrets/logging-auth.json,readonly" \
    -e STELLARMESH_LOGGING_AUTH_FILE=/run/secrets/logging-auth.json \
    -e STELLARMESH_LOGGING_KAFKA_BROKERS=kafka:9092 \
    -e STELLARMESH_LOGGING_KAFKA_TOPIC="$source_topic" \
    -e STELLARMESH_LOGGING_KAFKA_PUBLISH_TIMEOUT=2s \
    -e STELLARMESH_LOGGING_KAFKA_REPLAY_INTERVAL=1s \
    stellarmesh-logging-service:test >/dev/null
port_mapping="$(docker port "$ingester" 8091/tcp)"
ingester_port="${port_mapping##*:}"
[ -n "$ingester_port" ]
wait_until 'logging-service 就绪' curl -fsS "http://127.0.0.1:$ingester_port/health/ready"
[ "$(docker exec "$ingester" cat /var/lib/stellarmesh-logging/spool/FORMAT)" = 'stellarmesh-logging-spool-v2' ]

status="$(curl --max-time 10 -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$ingester_port/v2/log-events" \
    -H 'Content-Type: application/json' \
    -H "X-Logging-Service-Token: $service_token" \
    --data "{\"event\":{\"event_id\":\"99999999-9999-4999-8999-999999999999\",\"timestamp\":\"2026-08-01T12:00:00Z\",\"level\":\"INFO\",\"service\":\"integration-service\",\"message\":\"v1 event\",\"trace_id\":\"\",\"metadata\":{}}}")"
[ "$status" = '400' ]

curl -fsS -X POST "http://127.0.0.1:$ingester_port/v2/log-events" \
    -H 'Content-Type: application/json' \
    -H "X-Logging-Service-Token: $service_token" \
    --data "{\"event\":{\"event_id\":\"$event_id\",\"timestamp\":\"2026-08-01T12:00:00Z\",\"kind\":\"LOG\",\"level\":\"INFO\",\"service\":\"integration-service\",\"message\":\"integration event\",\"trace_id\":\"integration-trace\",\"metadata\":{\"source\":\"integration\"}}}" >/dev/null
curl -fsS -X POST "http://127.0.0.1:$ingester_port/v2/log-events" \
    -H 'Content-Type: application/json' \
    -H "X-Logging-Service-Token: $service_token" \
    --data "{\"event\":{\"event_id\":\"$audit_event_id\",\"timestamp\":\"2026-08-01T12:00:00Z\",\"kind\":\"AUDIT\",\"level\":\"INFO\",\"service\":\"integration-service\",\"message\":\"integration audit\",\"trace_id\":\"integration-trace\",\"metadata\":{\"action\":\"integration.verify\",\"outcome\":\"success\"}}}" >/dev/null
wait_until '事件写入 ClickHouse' clickhouse_has_event
wait_until '审计事件写入 ClickHouse' clickhouse_has_audit_event

printf '%s\n' '{"not":"a-log-event"}' | docker exec -i "$kafka" rpk topic produce "$source_topic" >/dev/null
dlq_payload="$(timeout 20 docker exec "$kafka" rpk topic consume "$dlq_topic" --num 1 --offset start --format '%v')"
printf '%s' "$dlq_payload" | grep -q '"reason":"invalid_event"'
printf '%s' "$dlq_payload" | grep -q '"source_topic":"stellarmesh.logging.events.v2"'

oversize_payload="$(dd if=/dev/zero bs=1024 count=1 2>/dev/null | tr '\000' x)"
printf '%s\n' "$oversize_payload" | docker exec -i "$kafka" rpk topic produce "$source_topic" >/dev/null
dlq_v2_payload="$(timeout 20 docker exec "$kafka" rpk topic consume "$dlq_topic" --num 1 --offset 1 --format '%v')"
printf '%s' "$dlq_v2_payload" | grep -q '"schema_version":"v2"'
printf '%s' "$dlq_v2_payload" | grep -q '"reason":"source_message_too_large"'
printf '%s' "$dlq_v2_payload" | grep -q '"content_omitted":true'

docker stop "$kafka" >/dev/null
status="$(curl --max-time 10 -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$ingester_port/v2/log-events" \
    -H 'Content-Type: application/json' \
    -H "X-Logging-Service-Token: $service_token" \
    --data "{\"event\":{\"event_id\":\"$spooled_event_id\",\"timestamp\":\"2026-08-01T12:00:01Z\",\"kind\":\"LOG\",\"level\":\"ERROR\",\"service\":\"integration-service\",\"message\":\"spooled integration event\",\"trace_id\":\"spooled-trace\",\"metadata\":{\"source\":\"integration\"}}}")"
[ "$status" = '202' ]
status="$(curl --max-time 10 -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$ingester_port/v2/log-events" \
    -H 'Content-Type: application/json' \
    -H "X-Logging-Service-Token: $service_token" \
    --data "{\"event\":{\"event_id\":\"$spooled_audit_event_id\",\"timestamp\":\"2026-08-01T12:00:02Z\",\"kind\":\"AUDIT\",\"level\":\"INFO\",\"service\":\"integration-service\",\"message\":\"spooled integration audit\",\"trace_id\":\"spooled-trace\",\"metadata\":{\"action\":\"integration.spool\",\"outcome\":\"success\"}}}")"
[ "$status" = '202' ]
wait_until 'Kafka 中断事件进入 spool' spool_has_segments

docker exec "$ingester" chmod 500 /var/lib/stellarmesh-logging/spool/.staging
status="$(curl --max-time 10 -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$ingester_port/v2/log-events" \
    -H 'Content-Type: application/json' \
    -H "X-Logging-Service-Token: $service_token" \
    --data "{\"event\":{\"event_id\":\"88888888-8888-4888-8888-888888888888\",\"timestamp\":\"2026-08-01T12:00:03Z\",\"kind\":\"LOG\",\"level\":\"ERROR\",\"service\":\"integration-service\",\"message\":\"durability unavailable\",\"trace_id\":\"\",\"metadata\":{}}}")"
[ "$status" = '503' ]
status="$(curl --max-time 10 -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$ingester_port/health/ready")"
[ "$status" = '503' ]
docker exec "$ingester" chmod 700 /var/lib/stellarmesh-logging/spool/.staging

docker start "$kafka" >/dev/null
wait_until 'Kafka 恢复' docker exec "$kafka" rpk cluster health --exit-when-healthy
wait_until 'spool 事件写入 ClickHouse' clickhouse_has_spooled_event
wait_until 'spool 审计事件写入 ClickHouse' clickhouse_has_spooled_audit_event
wait_until 'spool 回放完成' spool_is_empty
wait_until 'logging-service 恢复就绪' curl -fsS "http://127.0.0.1:$ingester_port/health/ready"

echo '日志链路、DLQ 与 Kafka 中断回放集成测试通过'
