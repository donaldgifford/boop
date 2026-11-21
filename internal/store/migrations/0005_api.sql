-- The API's reads (IMPL-0005 Phase 10): run_cause derives a run's cause
-- from the event its cause_key names, and an index serves the lookup
-- by key (0001 already indexes cause_key).

-- +goose Up
CREATE INDEX events_key_idx ON events (key);

-- run_cause is a RunCause: recheck for an action.recheck, webhook for a
-- delivery or a run-scheduled with a reason (an event's run), schedule
-- for a run-scheduled without one, unknown otherwise.
-- +goose StatementBegin
CREATE FUNCTION run_cause(cause text)
RETURNS text
LANGUAGE sql STABLE
AS $$
    SELECT CASE
        WHEN cause IS NULL OR cause = '' THEN 'unknown'
        WHEN cause LIKE 'delivery:%' THEN 'webhook'
        ELSE coalesce((
            SELECT CASE
                WHEN e.kind = 'action.recheck' THEN 'recheck'
                WHEN e.kind = 'repo.run-scheduled' AND coalesce(e.data->>'reason', '') <> '' THEN 'webhook'
                WHEN e.kind = 'repo.run-scheduled' THEN 'schedule'
                WHEN e.source = 'webhook' THEN 'webhook'
                ELSE 'unknown'
            END
            FROM event_keys k
            JOIN events e ON e.key = k.key AND e.occurred_at = k.occurred_at
            WHERE k.key = cause
            LIMIT 1
        ), 'unknown')
    END
$$;
-- +goose StatementEnd

UPDATE store_meta SET schema_version = 5;

-- +goose Down
UPDATE store_meta SET schema_version = 4;
DROP FUNCTION run_cause(text);
DROP INDEX events_key_idx;
