-- Grants to the roles the operator creates (DESIGN-0004 § Database roles,
-- OQ8). The migration runs as boopd_owner, which owns every object here.
-- No role but the owner can UPDATE or DELETE events.

-- +goose Up
GRANT USAGE ON SCHEMA public TO boopd_worker, boopd_ingest, boopd_ro;

-- Every role reads everything: the writers' applies read the rows they
-- upsert, and the API reads all of it.
GRANT SELECT ON ALL TABLES IN SCHEMA public TO boopd_worker, boopd_ingest, boopd_ro;

-- The log: append only.
GRANT INSERT ON events, event_keys TO boopd_worker, boopd_ingest;

-- The worker owns runs, updates, dependencies, repositories, discovery
-- and the reconciled pull requests and deliveries.
GRANT INSERT, UPDATE, DELETE ON
    runs, branch_updates, dependencies, repositories, installations,
    discovery_passes, pull_requests, webhook_deliveries
TO boopd_worker;

-- Partition maintenance runs as the owner through these functions.
GRANT EXECUTE ON FUNCTION ensure_partitions(integer), drop_partitions(timestamptz) TO boopd_worker;

-- Ingest writes deliveries, pull requests, installations and the
-- dashboard. dashboard.updated replaces a repository's items, so ingest
-- also deletes them.
GRANT INSERT, UPDATE ON webhook_deliveries, pull_requests, dashboard_items, dashboards, installations TO boopd_ingest;
GRANT DELETE ON dashboard_items TO boopd_ingest;

-- The API is read-only at the role. Setting a role's parameters needs
-- CREATEROLE, which a managed owner may not have; the API's pool sets the
-- same parameters on every connection, so a warning is enough here.
-- +goose StatementBegin
DO $$
BEGIN
    ALTER ROLE boopd_ro SET default_transaction_read_only = on;
    ALTER ROLE boopd_ro SET statement_timeout = '5s';
EXCEPTION WHEN insufficient_privilege THEN
    RAISE WARNING 'boopd_ro parameters not set: %', SQLERRM;
END;
$$;
-- +goose StatementEnd

UPDATE store_meta SET schema_version = 3;

-- +goose Down
UPDATE store_meta SET schema_version = 2;
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM boopd_worker, boopd_ingest, boopd_ro;
REVOKE EXECUTE ON FUNCTION ensure_partitions(integer), drop_partitions(timestamptz) FROM boopd_worker;
REVOKE USAGE ON SCHEMA public FROM boopd_worker, boopd_ingest, boopd_ro;
