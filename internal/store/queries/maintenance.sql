-- name: EnsurePartitions :one
-- Monthly events partitions from this month through months_ahead.
SELECT ensure_partitions(@months_ahead::integer)::integer;

-- name: DropPartitions :one
-- Drops the events partitions that end by before, and the default
-- partition's rows and the idempotency keys older than it.
SELECT drop_partitions(@before::timestamptz)::integer;

-- name: NullDeliveryPayloads :execrows
UPDATE webhook_deliveries
SET payload = NULL
WHERE payload IS NOT NULL
  AND received_at < @before::timestamptz;

-- name: DeleteRunsBefore :execrows
DELETE FROM runs WHERE started_at < @before::timestamptz;

-- name: OffboardedBefore :many
SELECT id FROM repositories WHERE offboarded_at < @before::timestamptz ORDER BY id;

-- name: DeleteRunsOf :execrows
DELETE FROM runs WHERE repo_id = ANY(@ids::bigint[]);

-- name: DeleteBranchUpdatesOf :execrows
DELETE FROM branch_updates WHERE repo_id = ANY(@ids::bigint[]);

-- name: DeleteDependenciesOf :execrows
DELETE FROM dependencies WHERE repo_id = ANY(@ids::bigint[]);

-- name: DeletePullRequestsOf :execrows
DELETE FROM pull_requests WHERE repo_id = ANY(@ids::bigint[]);

-- name: DeleteDashboardItemsOf :execrows
DELETE FROM dashboard_items WHERE repo_id = ANY(@ids::bigint[]);

-- name: DeleteDashboardsOf :execrows
DELETE FROM dashboards WHERE repo_id = ANY(@ids::bigint[]);

-- name: DeleteRepositories :execrows
DELETE FROM repositories WHERE id = ANY(@ids::bigint[]);
