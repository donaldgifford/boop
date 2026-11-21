-- The event log (DESIGN-0004 § The event log). Append-only: a row is
-- never updated or deleted, except by dropping a whole month partition
-- for retention.

-- +goose Up
CREATE TABLE events (
    id              bigint GENERATED ALWAYS AS IDENTITY,
    key             text        NOT NULL,
    occurred_at     timestamptz NOT NULL,
    recorded_at     timestamptz NOT NULL DEFAULT now(),
    kind            text        NOT NULL,
    source          text        NOT NULL,
    installation_id bigint,
    repo_id         bigint,
    pr_number       integer,
    actor_login     text,
    actor_type      text,
    cause_key       text,
    summary         text        NOT NULL DEFAULT '',
    data            jsonb       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (id, occurred_at)
) PARTITION BY RANGE (occurred_at);

-- Anything outside the monthly partitions, such as a replayed log older
-- than the first one, lands here.
CREATE TABLE events_default PARTITION OF events DEFAULT;

CREATE INDEX events_repo_occurred_idx ON events (repo_id, occurred_at DESC);
CREATE INDEX events_kind_occurred_idx ON events (kind, occurred_at DESC);
CREATE INDEX events_cause_key_idx ON events (cause_key);

-- A unique constraint on a partitioned table must include the partition
-- key, so the idempotency key (OQ7) is enforced here instead: Append
-- inserts the key first and skips the event when it already exists.
CREATE TABLE event_keys (
    key         text PRIMARY KEY,
    occurred_at timestamptz NOT NULL
);

CREATE INDEX event_keys_occurred_idx ON event_keys (occurred_at);

-- ensure_partitions creates the monthly partitions from the current
-- month through months_ahead months ahead. It is idempotent.
-- +goose StatementBegin
CREATE FUNCTION ensure_partitions(months_ahead integer)
RETURNS integer
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    start_month timestamptz := date_trunc('month', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC';
    lower_bound timestamptz;
    name        text;
    created     integer := 0;
BEGIN
    FOR i IN 0..months_ahead LOOP
        lower_bound := start_month + make_interval(months => i);
        name := 'events_' || to_char(lower_bound AT TIME ZONE 'UTC', 'YYYYMM');
        IF to_regclass('public.' || name) IS NULL THEN
            EXECUTE format(
                'CREATE TABLE public.%I PARTITION OF public.events FOR VALUES FROM (%L) TO (%L)',
                name, lower_bound, lower_bound + interval '1 month');
            created := created + 1;
        END IF;
    END LOOP;
    RETURN created;
END;
$$;
-- +goose StatementEnd

-- drop_partitions drops every monthly partition that ends at or before
-- the given time, and removes the default partition's rows and the
-- idempotency keys older than it.
-- +goose StatementBegin
CREATE FUNCTION drop_partitions(before timestamptz)
RETURNS integer
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    part    record;
    dropped integer := 0;
BEGIN
    FOR part IN
        SELECT c.relname AS name
        FROM pg_inherits i
        JOIN pg_class c ON c.oid = i.inhrelid
        WHERE i.inhparent = 'public.events'::regclass
          AND c.relname ~ '^events_[0-9]{6}$'
          AND to_timestamp(substr(c.relname, 8), 'YYYYMM') AT TIME ZONE 'UTC'
              + interval '1 month' <= before
    LOOP
        EXECUTE format('DROP TABLE public.%I', part.name);
        dropped := dropped + 1;
    END LOOP;

    DELETE FROM public.events_default WHERE occurred_at < before;
    DELETE FROM public.event_keys WHERE occurred_at < before;
    RETURN dropped;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION ensure_partitions(integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION drop_partitions(timestamptz) FROM PUBLIC;

SELECT ensure_partitions(3);

-- +goose Down
DROP FUNCTION drop_partitions(timestamptz);
DROP FUNCTION ensure_partitions(integer);
DROP TABLE event_keys;
DROP TABLE events;
