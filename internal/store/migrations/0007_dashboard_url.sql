-- The dashboard's issue URL, so the UI links a repository straight to
-- its Dependency Dashboard. dashboard.updated carries it from 0.14.0;
-- a dashboard recorded before has none until its next update.

-- +goose Up
UPDATE store_meta SET schema_version = 7;
ALTER TABLE dashboards ADD COLUMN issue_url text;

-- +goose Down
UPDATE store_meta SET schema_version = 6;
ALTER TABLE dashboards DROP COLUMN issue_url;
