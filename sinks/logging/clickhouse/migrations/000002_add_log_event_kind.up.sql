SELECT throwIf(
    count() > 0,
    'log_events contains an unsupported historical level'
)
FROM log_events
WHERE level NOT IN ('DEBUG', 'INFO', 'WARNING', 'ERROR', 'AUDIT');

ALTER TABLE log_events
    ADD COLUMN kind LowCardinality(String)
    DEFAULT if(level = 'AUDIT', 'AUDIT', 'LOG');

ALTER TABLE log_events
    MATERIALIZE COLUMN kind
    SETTINGS mutations_sync = 2;

ALTER TABLE log_events
    UPDATE level = 'INFO'
    WHERE level = 'AUDIT'
    SETTINGS mutations_sync = 2;

ALTER TABLE log_events
    MODIFY COLUMN kind LowCardinality(String)
    DEFAULT 'LOG';
