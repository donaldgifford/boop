-- The worker reconciles Dependency Dashboards from GitHub's issue
-- search (ReconcilePullRequests), for dashboards no webhook has shown
-- the store: Renovate edits its dashboard only when it changes. So the
-- worker writes the dashboard tables as ingest does. It already deletes
-- dashboard items for maintenance (0004).

-- +goose Up
UPDATE store_meta SET schema_version = 6;
GRANT INSERT, UPDATE ON dashboard_items, dashboards TO boopd_worker;

-- +goose Down
UPDATE store_meta SET schema_version = 5;
REVOKE INSERT, UPDATE ON dashboard_items, dashboards FROM boopd_worker;
