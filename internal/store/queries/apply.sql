-- name: UpsertDeliveryReceived :exec
INSERT INTO webhook_deliveries
    (delivery_id, attempt, app, event, action, outcome, installation_id, repo_id, received_at, payload)
VALUES
    (@delivery_id, @attempt, @app, @event, @action, @outcome,
     sqlc.narg(installation_id), sqlc.narg(repo_id), @received_at::timestamptz, sqlc.narg(payload))
ON CONFLICT (delivery_id, attempt) DO UPDATE SET
    app = excluded.app,
    event = excluded.event,
    action = excluded.action,
    outcome = excluded.outcome,
    installation_id = excluded.installation_id,
    repo_id = excluded.repo_id,
    received_at = excluded.received_at,
    payload = excluded.payload
WHERE webhook_deliveries.received_at IS NULL
   OR webhook_deliveries.received_at <= excluded.received_at;

-- name: UpsertDeliveryAudited :exec
INSERT INTO webhook_deliveries
    (delivery_id, attempt, app, event, action, github_status, missed, installation_id, repo_id, audited_at)
VALUES
    (@delivery_id, @attempt, @app, @event, @action, @github_status::integer, @missed,
     sqlc.narg(installation_id), sqlc.narg(repo_id), @audited_at::timestamptz)
ON CONFLICT (delivery_id, attempt) DO UPDATE SET
    github_status = excluded.github_status,
    missed = excluded.missed,
    audited_at = excluded.audited_at
WHERE webhook_deliveries.audited_at IS NULL
   OR webhook_deliveries.audited_at <= excluded.audited_at;

-- name: UpsertPullRequest :exec
INSERT INTO pull_requests
    (repo_id, number, branch, state, title, labels, automerge, url, opened_at, merged_at, closed_at, gh_updated_at)
VALUES
    (@repo_id, @number, @branch, @state, @title, @labels::text[], @automerge, @url,
     sqlc.narg(opened_at), sqlc.narg(merged_at), sqlc.narg(closed_at), @gh_updated_at)
ON CONFLICT (repo_id, number) DO UPDATE SET
    branch = excluded.branch,
    state = excluded.state,
    title = excluded.title,
    labels = excluded.labels,
    automerge = excluded.automerge,
    url = excluded.url,
    opened_at = excluded.opened_at,
    merged_at = excluded.merged_at,
    closed_at = excluded.closed_at,
    gh_updated_at = excluded.gh_updated_at
WHERE pull_requests.gh_updated_at <= excluded.gh_updated_at;

-- name: AdvanceDashboard :one
-- AdvanceDashboard moves the dashboard's guard forward. No row means the
-- update is older than the one already applied.
INSERT INTO dashboards (repo_id, updated_at)
VALUES (@repo_id, @updated_at)
ON CONFLICT (repo_id) DO UPDATE SET updated_at = excluded.updated_at
WHERE dashboards.updated_at <= excluded.updated_at
RETURNING repo_id;

-- name: UpsertDashboardItem :exec
-- UpsertDashboardItem keeps first_seen_at for an item that persists, and
-- a tick newer than this rewrite.
INSERT INTO dashboard_items
    (repo_id, marker, branch, section, title, deps, first_seen_at, last_seen_at)
VALUES
    (@repo_id, @marker, @branch, @section, @title, @deps, @seen_at, @seen_at)
ON CONFLICT (repo_id, marker, branch) DO UPDATE SET
    section = excluded.section,
    title = excluded.title,
    deps = excluded.deps,
    first_seen_at = least(dashboard_items.first_seen_at, excluded.first_seen_at),
    last_seen_at = excluded.last_seen_at,
    ticked_at = CASE WHEN dashboard_items.ticked_at > excluded.last_seen_at THEN dashboard_items.ticked_at END,
    ticked_by = CASE WHEN dashboard_items.ticked_at > excluded.last_seen_at THEN dashboard_items.ticked_by END;

-- name: DeleteStaleDashboardItems :exec
-- DeleteStaleDashboardItems drops the items the latest rewrite no longer
-- lists: every one it lists was just seen at seen_at.
DELETE FROM dashboard_items
WHERE repo_id = @repo_id AND last_seen_at < @seen_at;

-- name: TickDashboardItem :exec
UPDATE dashboard_items
SET ticked_at = @ticked_at::timestamptz, ticked_by = @ticked_by::text
WHERE repo_id = @repo_id AND marker = @marker AND branch = @branch
  AND last_seen_at <= @ticked_at
  AND (ticked_at IS NULL OR ticked_at <= @ticked_at);

-- name: EnsureRepository :exec
-- EnsureRepository creates a placeholder row for a repository first seen
-- through a run. Its lifecycle guard is -infinity, so any lifecycle
-- event, however old, still applies.
INSERT INTO repositories (id, installation_id, owner, slug, updated_at)
VALUES (@id, @installation_id, @owner, @slug, '-infinity')
ON CONFLICT (id) DO NOTHING;

-- name: UpsertRepositoryLifecycle :exec
-- UpsertRepositoryLifecycle applies repo.onboarded, repo.offboarded and
-- repo.refreshed; the dates not given are kept.
INSERT INTO repositories
    (id, installation_id, owner, slug, profile, onboarded_at, offboarded_at, updated_at)
VALUES
    (@id, @installation_id, @owner, @slug, @profile, sqlc.narg(onboarded_at), sqlc.narg(offboarded_at), @updated_at)
ON CONFLICT (id) DO UPDATE SET
    installation_id = excluded.installation_id,
    owner = excluded.owner,
    slug = excluded.slug,
    profile = CASE WHEN excluded.profile = '' THEN repositories.profile ELSE excluded.profile END,
    onboarded_at = coalesce(excluded.onboarded_at, repositories.onboarded_at),
    offboarded_at = CASE
        WHEN excluded.onboarded_at IS NOT NULL THEN NULL
        ELSE coalesce(excluded.offboarded_at, repositories.offboarded_at)
    END,
    updated_at = excluded.updated_at
WHERE repositories.updated_at <= excluded.updated_at;

-- name: ScheduleRepository :exec
UPDATE repositories
SET next_due = @next_due::timestamptz, scheduled_at = @scheduled_at::timestamptz
WHERE id = @id AND (scheduled_at IS NULL OR scheduled_at <= @scheduled_at);

-- name: SetRepositoryState :exec
UPDATE repositories
SET state = @state, state_at = @state_at::timestamptz
WHERE id = @id AND (state_at IS NULL OR state_at <= @state_at);

-- name: RecordRepositoryRun :one
-- RecordRepositoryRun sets the repository's last run. No row means a
-- newer run is already recorded, so its inventory stays.
UPDATE repositories
SET last_run_id = @run_id::text, last_run_at = @finished_at::timestamptz, last_outcome = @outcome::text
WHERE id = @id AND (last_run_at IS NULL OR last_run_at <= @finished_at)
RETURNING id;

-- name: UpsertRunStarted :exec
-- UpsertRunStarted records a run's start. When run.finished came first,
-- only what it lacks is filled in, so either order ends the same.
INSERT INTO runs (id, repo_id, cause_key, started_at, queued_ms, updated_at)
VALUES (@id, @repo_id, sqlc.narg(cause_key), @started_at, sqlc.narg(queued_ms), @started_at)
ON CONFLICT (id) DO UPDATE SET
    cause_key = coalesce(runs.cause_key, excluded.cause_key),
    queued_ms = coalesce(runs.queued_ms, excluded.queued_ms);

-- name: UpsertRunFinished :exec
INSERT INTO runs
    (id, repo_id, cause_key, outcome, repository_result, started_at, finished_at,
     duration_ms, renovate_version, spend, problems, updated_at)
VALUES
    (@id, @repo_id, sqlc.narg(cause_key), @outcome, @repository_result, @started_at, @finished_at::timestamptz,
     @duration_ms::bigint, @renovate_version, @spend, @problems, @finished_at)
ON CONFLICT (id) DO UPDATE SET
    cause_key = coalesce(excluded.cause_key, runs.cause_key),
    outcome = excluded.outcome,
    repository_result = excluded.repository_result,
    started_at = excluded.started_at,
    finished_at = excluded.finished_at,
    duration_ms = excluded.duration_ms,
    renovate_version = excluded.renovate_version,
    spend = excluded.spend,
    problems = excluded.problems,
    updated_at = excluded.updated_at
WHERE runs.updated_at <= excluded.updated_at;

-- name: DeleteBranchUpdates :exec
DELETE FROM branch_updates WHERE repo_id = @repo_id;

-- name: InsertBranchUpdate :batchexec
INSERT INTO branch_updates
    (repo_id, branch, package_file, package_name, run_id, pr_number, manager, datasource,
     dep_name, update_type, from_version, to_version, purl_from, purl_to)
VALUES
    (@repo_id, @branch, @package_file, @package_name, @run_id, sqlc.narg(pr_number), @manager, @datasource,
     @dep_name, @update_type, @from_version, @to_version, sqlc.narg(purl_from), sqlc.narg(purl_to))
ON CONFLICT (repo_id, branch, package_file, package_name) DO NOTHING;

-- name: DeleteDependencies :exec
DELETE FROM dependencies WHERE repo_id = @repo_id;

-- name: InsertDependency :batchexec
INSERT INTO dependencies
    (repo_id, manager, package_file, package_name, run_id, dep_name, datasource,
     current_value, current_version, purl)
VALUES
    (@repo_id, @manager, @package_file, @package_name, @run_id, @dep_name, @datasource,
     @current_value, @current_version, sqlc.narg(purl))
ON CONFLICT (repo_id, manager, package_file, package_name) DO NOTHING;

-- name: UpsertInstallation :exec
-- UpsertInstallation applies every installation.* kind. A removal keeps
-- the row and sets removed_at; any later kind clears it.
INSERT INTO installations (id, app, account_login, account_type, suspended, removed_at, updated_at)
VALUES (@id, @app, @account_login, @account_type, @suspended, sqlc.narg(removed_at), @updated_at)
ON CONFLICT (id) DO UPDATE SET
    app = CASE WHEN excluded.app = '' THEN installations.app ELSE excluded.app END,
    account_login = CASE WHEN excluded.account_login = '' THEN installations.account_login ELSE excluded.account_login END,
    account_type = CASE WHEN excluded.account_type = '' THEN installations.account_type ELSE excluded.account_type END,
    suspended = excluded.suspended,
    removed_at = excluded.removed_at,
    updated_at = excluded.updated_at
WHERE installations.updated_at <= excluded.updated_at;

-- name: UpsertDiscoveryPass :exec
-- UpsertDiscoveryPass records a pass; a later finish of the same pass wins.
INSERT INTO discovery_passes (app, started_at, finished_at, seen, onboarded, failed, probe_cost)
VALUES (@app, @started_at, @finished_at::timestamptz, @seen, @onboarded, @failed, @probe_cost)
ON CONFLICT (app, started_at) DO UPDATE SET
    finished_at = excluded.finished_at,
    seen = excluded.seen,
    onboarded = excluded.onboarded,
    failed = excluded.failed,
    probe_cost = excluded.probe_cost
WHERE discovery_passes.finished_at IS NULL
   OR discovery_passes.finished_at <= excluded.finished_at;
