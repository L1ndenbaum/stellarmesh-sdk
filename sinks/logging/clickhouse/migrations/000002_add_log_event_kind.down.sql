SELECT throwIf(
    count() > 0,
    'log_events contains an unsupported event kind'
)
FROM log_events
WHERE kind NOT IN ('LOG', 'AUDIT');

ALTER TABLE log_events
    UPDATE level = 'AUDIT'
    WHERE kind = 'AUDIT'
    SETTINGS mutations_sync = 2;

ALTER TABLE log_events
    DROP COLUMN kind;
