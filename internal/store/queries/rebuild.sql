-- name: TruncateProjections :exec
TRUNCATE installations, repositories, runs, branch_updates, dependencies,
    pull_requests, dashboard_items, webhook_deliveries, discovery_passes, dashboards;

-- name: ListEventsAfter :many
-- ListEventsAfter pages the log in (occurred_at, id) order, after the
-- given position.
SELECT id, key, occurred_at, kind, source, installation_id, repo_id, pr_number,
       actor_login, actor_type, cause_key, summary, data
FROM events
WHERE (occurred_at, id) > (@after_occurred_at::timestamptz, @after_id::bigint)
ORDER BY occurred_at, id
LIMIT @batch;
