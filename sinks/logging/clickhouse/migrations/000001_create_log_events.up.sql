CREATE TABLE IF NOT EXISTS log_events
(
    event_id UUID,
    timestamp DateTime64(3, 'UTC'),
    level LowCardinality(String),
    service LowCardinality(String),
    message String,
    trace_id String,
    metadata_json String,
    ingested_at DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY toYYYYMM(timestamp)
ORDER BY event_id;
