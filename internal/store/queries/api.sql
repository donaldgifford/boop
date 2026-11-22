-- The API role's reads (DESIGN-0004 § The API). Every query is scoped:
-- `(@all_owners::boolean OR lower(owner) = ANY(@owners::text[]))`, so a
-- row outside the caller's owners is indistinguishable from a missing
-- one. Lists are keyset-paged: the after_* arguments are the last row's
-- sort key, NULL on the first page, and lim is the page size plus one.

-- name: VisibleOwners :many
-- The installation accounts in scope that the store knows, for /me.
SELECT DISTINCT lower(account_login)::text AS owner
FROM installations
WHERE removed_at IS NULL
  AND (@all_owners::boolean OR lower(account_login) = ANY(@owners::text[]))
ORDER BY owner;

-- name: ListInstallations :many
-- Installations by id, with onboarded repositories and the spend of the
-- installation's latest finished run that recorded one.
SELECT i.id, i.app, i.account_login, i.account_type, i.suspended, i.removed_at,
       (SELECT count(*) FROM repositories r
        WHERE r.installation_id = i.id AND r.onboarded_at IS NOT NULL AND r.offboarded_at IS NULL)::int AS repositories,
       lr.spend AS last_spend, lr.finished_at AS last_finished_at
FROM installations i
LEFT JOIN LATERAL (
    SELECT ru.spend, ru.finished_at
    FROM runs ru
    JOIN repositories r ON r.id = ru.repo_id
    WHERE r.installation_id = i.id AND ru.finished_at IS NOT NULL AND ru.spend <> '{}'::jsonb
    ORDER BY ru.finished_at DESC
    LIMIT 1
) lr ON true
WHERE (@all_owners::boolean OR lower(i.account_login) = ANY(@owners::text[]))
  AND (sqlc.narg(after_id)::bigint IS NULL OR i.id > sqlc.narg(after_id)::bigint)
ORDER BY i.id
LIMIT @lim;

-- name: ListRepositories :many
-- Repositories by lower(slug), id, with open pull requests and unticked
-- dashboard items.
SELECT r.id, r.installation_id, r.owner, r.slug, r.state, r.profile, r.onboarded_at, r.offboarded_at,
       r.next_due, r.last_run_id, r.last_run_at, r.last_outcome,
       (SELECT count(*) FROM pull_requests p WHERE p.repo_id = r.id AND p.state = 'open')::int AS open_prs,
       (SELECT count(*) FROM dashboard_items d WHERE d.repo_id = r.id AND d.ticked_at IS NULL)::int AS waiting,
       lower(r.slug)::text AS sort_slug
FROM repositories r
WHERE (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[]))
  AND (sqlc.narg(id)::bigint IS NULL OR r.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(slug_prefix)::text IS NULL OR lower(r.slug) LIKE sqlc.narg(slug_prefix)::text)
  AND (sqlc.narg(state)::text IS NULL OR r.state = sqlc.narg(state)::text)
  AND (sqlc.narg(profile)::text IS NULL OR r.profile = sqlc.narg(profile)::text)
  AND (sqlc.narg(has_open_prs)::boolean IS NULL OR
       EXISTS (SELECT 1 FROM pull_requests p WHERE p.repo_id = r.id AND p.state = 'open') = sqlc.narg(has_open_prs)::boolean)
  AND (sqlc.narg(after_slug)::text IS NULL OR
       (lower(r.slug), r.id) > (sqlc.narg(after_slug)::text, sqlc.narg(after_id)::bigint))
ORDER BY lower(r.slug), r.id
LIMIT @lim;

-- name: ListEvents :many
-- The audit log, newest first. An event about a repository is visible
-- with its owner; one about none needs every owner.
SELECT e.id, e.key, e.occurred_at, e.recorded_at, e.kind, e.source, e.installation_id, e.repo_id,
       e.pr_number, e.actor_login, e.actor_type, e.cause_key, e.summary, e.data
FROM events e
WHERE (@all_owners::boolean OR EXISTS (
        SELECT 1 FROM repositories r WHERE r.id = e.repo_id AND lower(r.owner) = ANY(@owners::text[])))
  AND (sqlc.narg(repo_id)::bigint IS NULL OR e.repo_id = sqlc.narg(repo_id)::bigint)
  AND (sqlc.narg(kind)::text IS NULL OR e.kind = sqlc.narg(kind)::text OR
       (right(sqlc.narg(kind)::text, 1) = '.' AND starts_with(e.kind, sqlc.narg(kind)::text)))
  AND (sqlc.narg(source)::text IS NULL OR e.source = sqlc.narg(source)::text)
  AND (sqlc.narg(actor)::text IS NULL OR e.actor_login = sqlc.narg(actor)::text)
  AND (sqlc.narg(cause_key)::text IS NULL OR e.cause_key = sqlc.narg(cause_key)::text)
  AND (sqlc.narg(since)::timestamptz IS NULL OR e.occurred_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR e.occurred_at < sqlc.narg(until)::timestamptz)
  AND (sqlc.narg(after_at)::timestamptz IS NULL OR
       (e.occurred_at, e.id) < (sqlc.narg(after_at)::timestamptz, sqlc.narg(after_id)::bigint))
ORDER BY e.occurred_at DESC, e.id DESC
LIMIT @lim;

-- name: PullRequestTimeline :many
-- One pull request's events, oldest first, at most @lim.
SELECT e.id, e.key, e.occurred_at, e.recorded_at, e.kind, e.source, e.installation_id, e.repo_id,
       e.pr_number, e.actor_login, e.actor_type, e.cause_key, e.summary, e.data
FROM events e
WHERE e.repo_id = @repo_id AND e.pr_number = @number
ORDER BY e.occurred_at, e.id
LIMIT @lim;

-- name: ListRuns :many
-- Runs, newest first, with the repository's owner and slug, the derived
-- cause and the update count of the run's run.finished.
SELECT ru.id, ru.repo_id, r.owner, r.slug, run_cause(ru.cause_key)::text AS cause, ru.cause_key,
       ru.outcome, ru.repository_result, ru.started_at, ru.finished_at, ru.queued_ms, ru.duration_ms,
       ru.renovate_version, ru.spend, ru.problems,
       coalesce((SELECT jsonb_array_length(coalesce(e.data->'updates', '[]'::jsonb))
                 FROM events e
                 WHERE e.key = 'run:' || ru.id || ':finished' AND e.occurred_at = ru.finished_at
                 LIMIT 1), 0)::int AS updates
FROM runs ru
JOIN repositories r ON r.id = ru.repo_id
WHERE (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[]))
  AND (sqlc.narg(repo_id)::bigint IS NULL OR ru.repo_id = sqlc.narg(repo_id)::bigint)
  AND (sqlc.narg(outcome)::text IS NULL OR ru.outcome = sqlc.narg(outcome)::text)
  AND (sqlc.narg(cause)::text IS NULL OR run_cause(ru.cause_key) = sqlc.narg(cause)::text)
  AND (sqlc.narg(since)::timestamptz IS NULL OR ru.started_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR ru.started_at < sqlc.narg(until)::timestamptz)
  AND (sqlc.narg(after_at)::timestamptz IS NULL OR
       (ru.started_at, ru.id) < (sqlc.narg(after_at)::timestamptz, sqlc.narg(after_id)::text))
ORDER BY ru.started_at DESC, ru.id DESC
LIMIT @lim;

-- name: ListDependencies :many
-- Dependencies by repository, manager, file and name. purl_exact matches
-- a versioned purl; purl_prefix (a LIKE pattern) every version of one.
SELECT d.repo_id, r.owner, r.slug, d.manager, d.package_file, d.package_name, d.dep_name, d.datasource,
       d.current_value, d.current_version, d.purl
FROM dependencies d
JOIN repositories r ON r.id = d.repo_id
WHERE (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[]))
  AND (sqlc.narg(repo_id)::bigint IS NULL OR d.repo_id = sqlc.narg(repo_id)::bigint)
  AND (sqlc.narg(manager)::text IS NULL OR d.manager = sqlc.narg(manager)::text)
  AND (sqlc.narg(purl_exact)::text IS NULL OR d.purl = sqlc.narg(purl_exact)::text)
  AND (sqlc.narg(purl_prefix)::text IS NULL OR d.purl = sqlc.narg(purl_base)::text OR d.purl LIKE sqlc.narg(purl_prefix)::text)
  AND (sqlc.narg(after_repo)::bigint IS NULL OR
       (d.repo_id, d.manager, d.package_file, d.package_name) >
       (sqlc.narg(after_repo)::bigint, sqlc.narg(after_manager)::text, sqlc.narg(after_file)::text, sqlc.narg(after_name)::text))
ORDER BY d.repo_id, d.manager, d.package_file, d.package_name
LIMIT @lim;

-- name: ListUpdates :many
-- Proposed updates by repository, branch, file and name; purl matches
-- purl_from as in ListDependencies.
SELECT b.repo_id, r.owner, r.slug, b.branch, b.pr_number, b.package_file, b.package_name, b.dep_name,
       b.manager, b.datasource, b.update_type, b.from_version, b.to_version, b.purl_from, b.purl_to, b.run_id
FROM branch_updates b
JOIN repositories r ON r.id = b.repo_id
WHERE (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[]))
  AND (sqlc.narg(purl_exact)::text IS NULL OR b.purl_from = sqlc.narg(purl_exact)::text)
  AND (sqlc.narg(purl_prefix)::text IS NULL OR b.purl_from = sqlc.narg(purl_base)::text OR b.purl_from LIKE sqlc.narg(purl_prefix)::text)
  AND (sqlc.narg(from_version)::text IS NULL OR b.from_version = sqlc.narg(from_version)::text)
  AND (sqlc.narg(to_version)::text IS NULL OR b.to_version = sqlc.narg(to_version)::text)
  AND (sqlc.narg(update_type)::text IS NULL OR b.update_type = sqlc.narg(update_type)::text)
  AND (sqlc.narg(after_repo)::bigint IS NULL OR
       (b.repo_id, b.branch, b.package_file, b.package_name) >
       (sqlc.narg(after_repo)::bigint, sqlc.narg(after_branch)::text, sqlc.narg(after_file)::text, sqlc.narg(after_name)::text))
ORDER BY b.repo_id, b.branch, b.package_file, b.package_name
LIMIT @lim;

-- name: BranchUpdates :many
-- The updates on the given (repository, branch) pairs, for a page of
-- pull requests.
SELECT b.repo_id, r.owner, r.slug, b.branch, b.pr_number, b.package_file, b.package_name, b.dep_name,
       b.manager, b.datasource, b.update_type, b.from_version, b.to_version, b.purl_from, b.purl_to, b.run_id
FROM branch_updates b
JOIN repositories r ON r.id = b.repo_id
JOIN (SELECT unnest(@repo_ids::bigint[]) AS repo_id, unnest(@branches::text[]) AS branch) k
  ON k.repo_id = b.repo_id AND k.branch = b.branch
ORDER BY b.repo_id, b.branch, b.package_file, b.package_name;

-- name: ListPackages :many
-- Pending updates grouped by purl without version, most repositories
-- first.
WITH p AS (
    SELECT split_part(b.purl_from, '@', 1)::text AS purl,
           count(DISTINCT b.repo_id)::int AS repositories,
           count(*)::int AS updates,
           array_agg(DISTINCT b.from_version ORDER BY b.from_version)::text[] AS from_versions,
           array_agg(DISTINCT b.to_version ORDER BY b.to_version)::text[] AS to_versions,
           array_agg(DISTINCT b.manager ORDER BY b.manager)::text[] AS managers
    FROM branch_updates b
    JOIN repositories r ON r.id = b.repo_id
    WHERE b.purl_from IS NOT NULL
      AND (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[]))
      AND (sqlc.narg(manager)::text IS NULL OR b.manager = sqlc.narg(manager)::text)
    GROUP BY 1
)
SELECT p.purl, p.repositories, p.updates, p.from_versions, p.to_versions, p.managers
FROM p
WHERE sqlc.narg(after_repos)::int IS NULL
   OR p.repositories < sqlc.narg(after_repos)::int
   OR (p.repositories = sqlc.narg(after_repos)::int AND p.purl > sqlc.narg(after_purl)::text)
ORDER BY p.repositories DESC, p.purl
LIMIT @lim;

-- name: ListPullRequests :many
-- Pull requests, oldest opened first (GitHub's updated_at when never
-- seen opened).
SELECT p.repo_id, r.owner, r.slug, p.number, p.branch, p.state, p.title, p.labels, p.automerge, p.url,
       p.opened_at, p.merged_at, p.closed_at, p.gh_updated_at,
       coalesce(p.opened_at, p.gh_updated_at)::timestamptz AS sort_at
FROM pull_requests p
JOIN repositories r ON r.id = p.repo_id
WHERE (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[]))
  AND (sqlc.narg(repo_id)::bigint IS NULL OR p.repo_id = sqlc.narg(repo_id)::bigint)
  AND (sqlc.narg(number)::int IS NULL OR p.number = sqlc.narg(number)::int)
  AND (sqlc.narg(state)::text IS NULL OR p.state = sqlc.narg(state)::text)
  AND (sqlc.narg(automerge)::boolean IS NULL OR p.automerge = sqlc.narg(automerge)::boolean)
  AND (sqlc.narg(label)::text IS NULL OR sqlc.narg(label)::text = ANY(p.labels))
  AND (sqlc.narg(opened_before)::timestamptz IS NULL OR p.opened_at < sqlc.narg(opened_before)::timestamptz)
  AND (sqlc.narg(manager)::text IS NULL OR EXISTS (
        SELECT 1 FROM branch_updates b
        WHERE b.repo_id = p.repo_id AND b.branch = p.branch AND b.manager = sqlc.narg(manager)::text))
  AND (sqlc.narg(update_type)::text IS NULL OR EXISTS (
        SELECT 1 FROM branch_updates b
        WHERE b.repo_id = p.repo_id AND b.branch = p.branch AND b.update_type = sqlc.narg(update_type)::text))
  AND (sqlc.narg(purl_exact)::text IS NULL OR EXISTS (
        SELECT 1 FROM branch_updates b
        WHERE b.repo_id = p.repo_id AND b.branch = p.branch AND b.purl_from = sqlc.narg(purl_exact)::text))
  AND (sqlc.narg(purl_prefix)::text IS NULL OR EXISTS (
        SELECT 1 FROM branch_updates b
        WHERE b.repo_id = p.repo_id AND b.branch = p.branch
          AND (b.purl_from = sqlc.narg(purl_base)::text OR b.purl_from LIKE sqlc.narg(purl_prefix)::text)))
  AND (sqlc.narg(after_at)::timestamptz IS NULL OR
       (coalesce(p.opened_at, p.gh_updated_at), p.repo_id, p.number) >
       (sqlc.narg(after_at)::timestamptz, sqlc.narg(after_repo)::bigint, sqlc.narg(after_number)::int))
ORDER BY coalesce(p.opened_at, p.gh_updated_at), p.repo_id, p.number
LIMIT @lim;

-- name: ListDashboardItems :many
-- Dependency Dashboard items, oldest first seen first.
SELECT d.repo_id, r.owner, r.slug, d.marker, d.branch, d.section, d.title, d.deps, d.first_seen_at,
       d.last_seen_at, d.ticked_at, d.ticked_by
FROM dashboard_items d
JOIN repositories r ON r.id = d.repo_id
WHERE (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[]))
  AND (sqlc.narg(repo_id)::bigint IS NULL OR d.repo_id = sqlc.narg(repo_id)::bigint)
  AND (sqlc.narg(section)::text IS NULL OR d.section = sqlc.narg(section)::text)
  AND (sqlc.narg(seen_before)::timestamptz IS NULL OR d.first_seen_at < sqlc.narg(seen_before)::timestamptz)
  AND (sqlc.narg(after_at)::timestamptz IS NULL OR
       (d.first_seen_at, d.repo_id, d.marker, d.branch) >
       (sqlc.narg(after_at)::timestamptz, sqlc.narg(after_repo)::bigint, sqlc.narg(after_marker)::text, sqlc.narg(after_branch)::text))
ORDER BY d.first_seen_at, d.repo_id, d.marker, d.branch
LIMIT @lim;

-- name: ListDeliveries :many
-- Webhook deliveries, newest first by received time (audit time for a
-- delivery ingest never recorded). Unscoped: the handler needs every
-- owner.
SELECT w.delivery_id, w.attempt, w.app, w.event, w.action, w.outcome, w.github_status, w.missed,
       w.installation_id, w.repo_id, w.received_at, w.audited_at,
       coalesce(w.received_at, w.audited_at, 'epoch'::timestamptz)::timestamptz AS sort_at
FROM webhook_deliveries w
WHERE (sqlc.narg(event)::text IS NULL OR w.event = sqlc.narg(event)::text)
  AND (sqlc.narg(outcome)::text IS NULL OR w.outcome = sqlc.narg(outcome)::text)
  AND (sqlc.narg(github_status)::int IS NULL OR w.github_status = sqlc.narg(github_status)::int)
  AND (sqlc.narg(missed)::boolean IS NULL OR w.missed = sqlc.narg(missed)::boolean)
  AND (sqlc.narg(after_at)::timestamptz IS NULL OR
       (coalesce(w.received_at, w.audited_at, 'epoch'::timestamptz), w.delivery_id::text, w.attempt) <
       (sqlc.narg(after_at)::timestamptz, sqlc.narg(after_delivery)::text, sqlc.narg(after_attempt)::int))
ORDER BY coalesce(w.received_at, w.audited_at, 'epoch'::timestamptz) DESC, w.delivery_id::text DESC, w.attempt DESC
LIMIT @lim;

-- name: EstateNumbers :one
-- The estate's point-in-time numbers for a scope (DESIGN-0004 § Estate).
SELECT
    (SELECT count(*) FROM repositories r
     WHERE r.onboarded_at IS NOT NULL AND r.offboarded_at IS NULL
       AND (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[])))::int AS repositories,
    (SELECT count(*) FROM pull_requests p JOIN repositories r ON r.id = p.repo_id
     WHERE p.state = 'open' AND (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[])))::int AS open_prs,
    (SELECT count(*) FROM dashboard_items d JOIN repositories r ON r.id = d.repo_id
     WHERE d.ticked_at IS NULL AND (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[])))::int AS waiting,
    (SELECT min(p.opened_at) FROM pull_requests p JOIN repositories r ON r.id = p.repo_id
     WHERE p.state = 'open' AND (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[]))) AS oldest_open,
    (SELECT count(*) FROM runs ru JOIN repositories r ON r.id = ru.repo_id
     WHERE ru.started_at >= @day_start::timestamptz AND (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[])))::int AS runs_24h,
    (SELECT count(*) FROM runs ru JOIN repositories r ON r.id = ru.repo_id
     WHERE ru.finished_at >= @day_start::timestamptz AND ru.outcome <> ''
       AND (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[])))::int AS finished_24h,
    (SELECT count(*) FROM runs ru JOIN repositories r ON r.id = ru.repo_id
     WHERE ru.finished_at >= @day_start::timestamptz AND ru.outcome = 'Succeeded'
       AND (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[])))::int AS succeeded_24h,
    (SELECT count(*) FROM webhook_deliveries w LEFT JOIN installations i ON i.id = w.installation_id
     WHERE coalesce(w.received_at, w.audited_at) >= @day_start::timestamptz
       AND (w.missed OR w.github_status = 0 OR w.github_status >= 400)
       AND (@all_owners::boolean OR lower(i.account_login) = ANY(@owners::text[])))::int AS lost_deliveries_24h,
    (SELECT count(*) FROM pull_requests p JOIN repositories r ON r.id = p.repo_id
     WHERE p.merged_at >= @range_start::timestamptz AND (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[])))::int AS merged_in_range;

-- name: EstateAgeBuckets :one
-- Open pull requests by age: under a day, a week, 30 days, 90 days, and
-- older. The boundaries are opened_at thresholds: d1 is a day before
-- now, and so on.
SELECT
    coalesce(sum(CASE WHEN p.opened_at > @d1::timestamptz THEN 1 ELSE 0 END), 0)::int AS under_1d,
    coalesce(sum(CASE WHEN p.opened_at <= @d1::timestamptz AND p.opened_at > @d7::timestamptz THEN 1 ELSE 0 END), 0)::int AS under_7d,
    coalesce(sum(CASE WHEN p.opened_at <= @d7::timestamptz AND p.opened_at > @d30::timestamptz THEN 1 ELSE 0 END), 0)::int AS under_30d,
    coalesce(sum(CASE WHEN p.opened_at <= @d30::timestamptz AND p.opened_at > @d90::timestamptz THEN 1 ELSE 0 END), 0)::int AS under_90d,
    coalesce(sum(CASE WHEN p.opened_at <= @d90::timestamptz THEN 1 ELSE 0 END), 0)::int AS over_90d
FROM pull_requests p
JOIN repositories r ON r.id = p.repo_id
WHERE p.state = 'open' AND p.opened_at IS NOT NULL
  AND (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[]));

-- name: EstateEcosystems :many
-- Open pull requests by Renovate manager, most first.
SELECT b.manager::text AS label, count(DISTINCT (p.repo_id, p.number))::int AS count
FROM pull_requests p
JOIN repositories r ON r.id = p.repo_id
JOIN branch_updates b ON b.repo_id = p.repo_id AND b.branch = p.branch
WHERE p.state = 'open' AND (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[]))
GROUP BY b.manager
ORDER BY count DESC, label;

-- name: SeriesRuns :many
-- Runs started per bucket and outcome.
SELECT date_bin(@step::interval, ru.started_at, @origin::timestamptz)::timestamptz AS at, ru.outcome, count(*)::int AS count
FROM runs ru
JOIN repositories r ON r.id = ru.repo_id
WHERE ru.started_at >= @origin::timestamptz
  AND (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[]))
GROUP BY 1, 2
ORDER BY 1, 2;

-- name: SeriesMerged :many
-- Pull requests merged per bucket.
SELECT date_bin(@step::interval, p.merged_at, @origin::timestamptz)::timestamptz AS at, count(*)::int AS count
FROM pull_requests p
JOIN repositories r ON r.id = p.repo_id
WHERE p.merged_at >= @origin::timestamptz
  AND (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[]))
GROUP BY 1
ORDER BY 1;

-- name: SeriesOpenPRs :many
-- Pull requests open at each bucket's end.
SELECT g.at::timestamptz AS at, count(p.repo_id)::int AS count
FROM generate_series(@origin::timestamptz, @last::timestamptz, @step::interval) AS g(at)
LEFT JOIN (
    SELECT p.repo_id, p.opened_at, coalesce(p.merged_at, p.closed_at) AS ended_at
    FROM pull_requests p JOIN repositories r ON r.id = p.repo_id
    WHERE p.opened_at IS NOT NULL AND (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[]))
) p ON p.opened_at < g.at + @step::interval AND (p.ended_at IS NULL OR p.ended_at >= g.at + @step::interval)
GROUP BY g.at
ORDER BY g.at;

-- name: SeriesWaiting :many
-- Dashboard items unticked at each bucket's end.
SELECT g.at::timestamptz AS at, count(d.repo_id)::int AS count
FROM generate_series(@origin::timestamptz, @last::timestamptz, @step::interval) AS g(at)
LEFT JOIN (
    SELECT d.repo_id, d.first_seen_at, d.ticked_at
    FROM dashboard_items d JOIN repositories r ON r.id = d.repo_id
    WHERE (@all_owners::boolean OR lower(r.owner) = ANY(@owners::text[]))
) d ON d.first_seen_at < g.at + @step::interval AND (d.ticked_at IS NULL OR d.ticked_at >= g.at + @step::interval)
GROUP BY g.at
ORDER BY g.at;

-- name: StatusNumbers :one
-- The status page's aggregates: fleet totals, deliveries in the last
-- 24 h, and the ages of the latest delivery and discovery pass.
SELECT
    (SELECT count(*) FROM repositories r WHERE r.onboarded_at IS NOT NULL AND r.offboarded_at IS NULL)::int AS repositories,
    (SELECT count(*) FROM pull_requests p WHERE p.state = 'open')::int AS open_prs,
    (SELECT count(*) FROM runs ru WHERE ru.started_at >= @day_start::timestamptz)::int AS runs_24h,
    (SELECT count(*) FROM webhook_deliveries w
     WHERE coalesce(w.received_at, w.audited_at) >= @day_start::timestamptz)::int AS deliveries_24h,
    (SELECT count(*) FROM webhook_deliveries w
     WHERE coalesce(w.received_at, w.audited_at) >= @day_start::timestamptz AND w.github_status IS NOT NULL
       AND (w.github_status = 0 OR w.github_status >= 400))::int AS failed_24h,
    (SELECT count(*) FROM webhook_deliveries w
     WHERE coalesce(w.received_at, w.audited_at) >= @day_start::timestamptz AND w.missed)::int AS missed_24h,
    (SELECT max(w.received_at) FROM webhook_deliveries w) AS last_delivery,
    (SELECT max(dp.finished_at) FROM discovery_passes dp) AS last_discovery;

-- name: LatestSpends :many
-- Each installation's latest finished run's spend, for the status page's
-- GitHub budget.
SELECT DISTINCT ON (r.installation_id) r.installation_id, ru.spend
FROM runs ru
JOIN repositories r ON r.id = ru.repo_id
WHERE ru.finished_at IS NOT NULL AND ru.spend <> '{}'::jsonb
ORDER BY r.installation_id, ru.finished_at DESC;
