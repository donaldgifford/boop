-- Retention (IMPL-0005 Phase 8): StoreMaintenanceWorkflow runs as
-- boopd_worker and deletes an offboarded repository's projections,
-- its dashboard included, which ingest otherwise owns.

-- +goose Up
GRANT DELETE ON dashboard_items, dashboards TO boopd_worker;

UPDATE store_meta SET schema_version = 4;

-- +goose Down
UPDATE store_meta SET schema_version = 3;
REVOKE DELETE ON dashboard_items, dashboards FROM boopd_worker;
