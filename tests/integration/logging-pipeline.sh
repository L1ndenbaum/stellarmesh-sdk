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
source_topic='stellarmesh.logging.events.v1'
dlq_topic='stellarmesh.logging.events.v1.dlq'
event_id='11111111-1111-4111-8111-111111111111'
spooled_event_id='22222222-2222-4222-8222-222222222222'
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
    count="$(docker exec "$clickhouse" clickhouse-client --database logging --query "SELECT count() FROM log_events WHERE event_id = '$event_id'")"
    [ "$count" = '1' ]
}

clickhouse_has_spooled_event() {
    count="$(docker exec "$clickhouse" clickhouse-client --database logging --query "SELECT count() FROM log_events WHERE event_id = '$spooled_event_id'")"
    [ "$count" = '1' ]
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
wait_until 'Kafka 就绪' docker exec "$kafka" rpk cluster health --exit-when-healthy
docker exec "$kafka" rpk topic create "$source_topic" "$dlq_topic" >/dev/null

docker run --rm --network "$network" stellarmesh-logging-clickhouse-migrate:test \
    -database 'clickhouse://clickhouse:9000?username=default&database=logging&x-multi-statement=true' up

docker run -d --name "$sink" --network "$network" \
    -e STELLARMESH_LOGGING_KAFKA_BROKERS=kafka:9092 \
    -e STELLARMESH_LOGGING_KAFKA_TOPIC="$source_topic" \
    -e STELLARMESH_LOGGING_KAFKA_DLQ_TOPIC="$dlq_topic" \
    -e STELLARMESH_LOGGING_CLICKHOUSE_HTTP_URL=http://clickhouse:8123 \
    -e STELLARMESH_LOGGING_CLICKHOUSE_DATABASE=logging \
    -e STELLARMESH_LOGGING_CLICKHOUSE_USER=default \
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
    -e STELLARMESH_LOGGING_CONSOLE_COLOR=false \
    stellarmesh-logging-service:test >/dev/null
port_mapping="$(docker port "$ingester" 8091/tcp)"
ingester_port="${port_mapping##*:}"
[ -n "$ingester_port" ]
wait_until 'logging-service 就绪' curl -fsS "http://127.0.0.1:$ingester_port/health/ready"

curl -fsS -X POST "http://127.0.0.1:$ingester_port/v1/log-events" \
    -H 'Content-Type: application/json' \
    -H "X-Logging-Service-Token: $service_token" \
    --data "{\"event\":{\"event_id\":\"$event_id\",\"timestamp\":\"2026-08-01T12:00:00Z\",\"level\":\"INFO\",\"service\":\"integration-service\",\"message\":\"integration event\",\"trace_id\":\"integration-trace\",\"metadata\":{\"source\":\"integration\"}}}" >/dev/null
wait_until '事件写入 ClickHouse' clickhouse_has_event

printf '%s\n' '{"not":"a-log-event"}' | docker exec -i "$kafka" rpk topic produce "$source_topic" >/dev/null
dlq_payload="$(timeout 20 docker exec "$kafka" rpk topic consume "$dlq_topic" --num 1 --offset start --format '%v')"
printf '%s' "$dlq_payload" | grep -q '"reason":"invalid_event"'
printf '%s' "$dlq_payload" | grep -q '"source_topic":"stellarmesh.logging.events.v1"'

docker stop "$kafka" >/dev/null
status="$(curl --max-time 10 -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$ingester_port/v1/log-events" \
    -H 'Content-Type: application/json' \
    -H "X-Logging-Service-Token: $service_token" \
    --data "{\"event\":{\"event_id\":\"$spooled_event_id\",\"timestamp\":\"2026-08-01T12:00:01Z\",\"level\":\"ERROR\",\"service\":\"integration-service\",\"message\":\"spooled integration event\",\"trace_id\":\"spooled-trace\",\"metadata\":{\"source\":\"integration\"}}}")"
[ "$status" = '202' ]
wait_until 'Kafka 中断事件进入 spool' spool_has_segments

docker start "$kafka" >/dev/null
wait_until 'Kafka 恢复' docker exec "$kafka" rpk cluster health --exit-when-healthy
wait_until 'spool 事件写入 ClickHouse' clickhouse_has_spooled_event
wait_until 'spool 回放完成' spool_is_empty
wait_until 'logging-service 恢复就绪' curl -fsS "http://127.0.0.1:$ingester_port/health/ready"

echo '日志链路、DLQ 与 Kafka 中断回放集成测试通过'
