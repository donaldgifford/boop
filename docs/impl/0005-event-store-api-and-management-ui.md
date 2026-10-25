---
id: IMPL-0005
title: "Event store, API and management UI"
status: Draft
author: Donald Gifford
created: 2026-10-18
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0005: Event store, API and management UI

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Findings (2026-10-18)](#findings-2026-10-18)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Decisions, toolchain and the test database](#phase-1-decisions-toolchain-and-the-test-database)
  - [Phase 2: Schema, roles and migrate](#phase-2-schema-roles-and-migrate)
  - [Phase 3: Append, applies and rebuild](#phase-3-append-applies-and-rebuild)
  - [Phase 4: purl, report dependencies and the dashboard parser](#phase-4-purl-report-dependencies-and-the-dashboard-parser)
  - [Phase 5: Worker writers](#phase-5-worker-writers)
  - [Phase 6: Ingest and the audit into the store](#phase-6-ingest-and-the-audit-into-the-store)
  - [Phase 7: Pull request reconcile](#phase-7-pull-request-reconcile)
  - [Phase 8: Retention](#phase-8-retention)
  - [Phase 9: The API contract and server](#phase-9-the-api-contract-and-server)
  - [Phase 10: API endpoints, recheck, status and estate](#phase-10-api-endpoints-recheck-status-and-estate)
  - [Phase 11: The UI scaffold, copied](#phase-11-the-ui-scaffold-copied)
  - [Phase 12: UI pages](#phase-12-ui-pages)
  - [Phase 13: Chart](#phase-13-chart)
  - [Phase 14: End to end](#phase-14-end-to-end)
  - [Phase 15: CI, release and documentation](#phase-15-ci-release-and-documentation)
  - [Phase 16: PRs, releases and the homelab](#phase-16-prs-releases-and-the-homelab)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [Resolved Questions](#resolved-questions)
- [References](#references)
<!--toc:end-->

## Objective

Build DESIGN-0004 (all OQs resolved; OQ2 "copy, never depend"):

- **A Postgres event log** (`events`, append-only), with projections
  applied in the same transaction and rebuildable by replay. The
  projections are repositories, runs, proposed updates, current
  dependencies, PRs, dashboard items, deliveries and discovery passes.
- **Writers:** the worker (through activities), ingest (directly, with
  its own role) and the delivery audit. The API is read-only.
- **`/api/v1`:** OpenAPI 3.1 with OIDC bearer authentication, owner
  visibility plus an `operators` list, keyset cursors, RFC 9457 errors,
  one action (recheck), a public aggregate status, and estate numbers.
- **`boopd-ui`:** a Bun + Hono BFF and React 19 SPA with repo-guardian
  v2's pattern, **copied** into `ui/`. It has the Estate, Pull requests,
  Waiting, Packages, Repositories, Repository, Runs, Events, Deliveries
  and Status pages.
- **Chart support** for the store and migrate Job, `api`, `ui` and their
  NetworkPolicies. All of it is off by default.

**Implements:** DESIGN-0004 and ADR-0006. It completes DESIGN-0003's
recording (log sink → store; "missed" deliveries).

## Scope

### In Scope

- New Go packages:
  - `internal/store`: pgx, goose migrations, sqlc queries, `Append`, the
    applies, rebuild, retention;
  - `internal/purl`;
  - `internal/renovate/dashboard`;
  - `internal/api`: the generated server, middleware, authn, authz,
    cursor, problem, handlers, caches.
- Changes to existing Go packages:
  - `internal/renovate`: the report's `packageFiles[].deps[]`;
  - `internal/activities` and `internal/workflows`: `RecordRun`,
    `RecordEvents`, `ReconcilePullRequests`,
    `StoreMaintenanceWorkflow`, the `recheck` payload, the decision
    events;
  - `internal/ingest`: a store `Sink` and the PR and dashboard events.
- Commands: `boopd migrate`, `boopd api`, `boopd store rebuild`.
- `api/openapi.yaml`, `api/oapi-codegen.yaml`, `api/vacuum.yaml`.
- `ui/`: the BFF, the SPA, tests, the Dockerfile, and the design mockup.
- `docker-bake.hcl`, `charts/boopd`, `.github/workflows`, `justfile`,
  `mise.toml`.
- Docs: `docs/config.md`, `docs/api.md`, `docs/ui.md`, and notes on
  ADR-0006 and DESIGN-0003.

### Out of Scope

- **The security correlator and `findings`** (a later design). The
  `purl` columns and the `/updates`, `/dependencies` and `/packages`
  filters are the hand-off.
- **Redeliver and approve actions,** and evals.
- **A shared design system or combined UI** (DESIGN-0004 non-goals).
- **Postgres provisioning** (CNPG is the operator's, DESIGN-0004 OQ15).

## Findings (2026-10-18)

From reading the code for this plan:

- **IMPL-0004 lands first** (DESIGN-0004 OQ16). Phase 6 here depends on
  its `ingest.Sink`, its `Record` type, the delivery audit and the
  decision log points in `RepoWorkflow`.
- **The report parser reads only `packageFiles` keys**
  (`internal/renovate/report.go`, `reportPackageFile{PackageFile}`).
  Phase 4 extends the struct with `deps[]`. The real Renovate 44
  fixtures (`internal/renovate/testdata/renovate-44-*.log`, added
  2026-10-23) already carry `packageFiles[].deps[]`, 140 in the live
  report.
- **`RunResult` is in workflow history.** Adding `Dependencies` grows
  every run's result. A large repository has hundreds of deps (OQ5).
- **`recheck` discards payloads** (`c.Receive(ctx, nil)`). The actor
  payload must keep the CLI's no-payload signal valid (OQ9).
- **There is no Postgres anywhere in the repository yet.** There are no
  pgx, goose or sqlc modules, no test database, and no e2e Postgres
  (OQ2, OQ3, OQ10).
- **repo-guardian v2 @ `a72faf9`** is the copy source. It is not on
  repo-guardian's `main`, and by 2026-10-23 its `ui/` had five newer
  commits, including repository-page fixes and Tabler Icons behind an
  `Icon` component (INV-0024), where DESIGN-0004 says inline SVG icons.
  Re-pin to the newest commit before Phase 11 and update DESIGN-0004's
  icon row to match. Its `ui/` uses
  Bun 1.3.14, React 19.3, Vite 8, Tailwind v4, TanStack Router 1.170 and
  Query 5, openapi-typescript and openapi-fetch, Hono 4, openid-client
  6, jose 6, Recharts 3, Radix and `@fontsource`. On the Go side it uses
  oapi-codegen v2.8.0 (as a `go tool`), kin-openapi, go-oidc v3,
  pgx/v5, goose v3.28 and sqlc. Each copied file gets a provenance
  header (OQ6).
- **There is one image and one bake group today.** `boopd-ui` adds
  targets and a second release artefact with the same tag, and
  `release.yml` must sign and attest it like `boopd`.
- **Exposure:** the UI stays tailnet-only through the Tailscale ingress.
  The "public" status page is unauthenticated, not internet-facing,
  unless Funnel is added later (OQ14).

## Implementation Phases

Each phase ends with `just lint`, `just test` and its own checks green,
with one commit per numbered task. The PR split is in OQ1.

### Phase 1: Decisions, toolchain and the test database

#### Tasks

- [ ] 1.1 Status changes: DESIGN-0004 → Approved; ADR-0006 → Accepted,
  with "realised by DESIGN-0004".
- [ ] 1.2 Go modules: `github.com/jackc/pgx/v5`,
  `github.com/pressly/goose/v3`, `github.com/coreos/go-oidc/v3`,
  `github.com/getkin/kin-openapi`. Tools as `go tool` in `go.mod`:
  `oapi-codegen` v2.8.0 and `sqlc` (OQ4).
- [ ] 1.3 `mise.toml`: pins for `bun` (1.3.14) and `vacuum`. Playwright
  runs from its pinned image, not mise.
- [ ] 1.4 Test database (OQ2): `internal/store/storetest` starts a
  pinned `postgres:17` container through Docker for tests under the
  `integration` tag, like `temporaltest` for the dev server. `just
  test-integration` covers it, and CI's integration job gets Docker.
- [ ] 1.5 `justfile` recipes: `generate-sql`, `generate-api`, `lint-api`
  (vacuum plus drift), `lint-sql` (sqlc vet plus drift), and `ui-*`
  (install, lint, test, build, e2e, visual).

#### Success Criteria

- `go build ./...` with the new modules.
- `just test-integration` starts and stops the test database.
- `just --list` shows the new recipes.

### Phase 2: Schema, roles and migrate

#### Tasks

- [ ] 2.1 `internal/store/migrations/0001_events.sql`:
  - `events`, partitioned by month on `occurred_at`;
  - a unique `key` (OQ7);
  - the indexes from DESIGN-0004;
  - `ensure_partitions(months_ahead int)` and `drop_partitions(before
    timestamptz)` as `SECURITY DEFINER` functions.
- [ ] 2.2 `0002_projections.sql`:
  - `installations`, `repositories`, `runs`;
  - `branch_updates`, `dependencies`;
  - `pull_requests`, `dashboard_items`;
  - `webhook_deliveries`, `discovery_passes`;
  - `store_meta(schema_version)`.

  Keys and columns are as in DESIGN-0004 § Tables.
- [ ] 2.3 `0003_grants.sql`: grants to the existing roles `boopd_worker`,
  `boopd_ingest` and `boopd_ro` (OQ8). `events` has no UPDATE or DELETE
  for anyone but the owner, and `boopd_ro` has
  `default_transaction_read_only`.
- [ ] 2.4 `internal/store/migrate.go`: a goose Provider over the
  embedded files, under an advisory lock, with the version table
  `goose_boopd_version`. `RequireVersion(ctx, pool, n)` makes a role
  refuse to start below the schema it needs.
- [ ] 2.5 `boopd migrate --config <file>` in `cmd/boopd`, using the
  owner DSN from `store.owner_dsn_secret_ref` (migrate only).
- [ ] 2.6 Tests (integration): up from empty and idempotent re-run;
  role grants (a `boopd_ro` INSERT fails, a `boopd_ingest` UPDATE on
  `events` fails); partition functions.

#### Success Criteria

- `boopd migrate` applies the schema to the test database twice without
  error.
- The grant tests prove that `events` is append-only for every writer
  role.

### Phase 3: Append, applies and rebuild

#### Tasks

- [ ] 3.1 `internal/store/event.go`: `Event{Key, OccurredAt, Kind,
  Source, InstallationID, RepoID, PRNumber, Actor, CauseKey, Summary,
  Data}`. `Data` is a typed struct per kind with `V int`, registered in
  a `kinds` table (OQ5).
- [ ] 3.2 `internal/store/append.go`: `Append(ctx, events ...Event)`.
  - In one transaction it runs `INSERT … ON CONFLICT (key) DO NOTHING
    RETURNING id`, then the kind's apply for each inserted row.
  - A duplicate skips its apply.
  - It updates `boopd_store_append_total{role,kind,outcome}` and
    `boopd_store_append_seconds`.
- [ ] 3.3 sqlc queries in `internal/store/queries/*.sql` and the
  generated package `internal/store/db`. jsonb maps to `json.RawMessage`.
- [ ] 3.4 Applies (`internal/store/apply_*.go`), one per kind in the
  DESIGN-0004 catalogue. Each applies the order guard: it compares the
  source timestamp, or `gh_updated_at` for PRs, and ignores older data.
- [ ] 3.5 `boopd store rebuild --config <file>`: truncate the
  projections, then stream `events` in `(occurred_at, id)` order through
  the same applies, in batches. The owner DSN is required.
- [ ] 3.6 Tests (integration), per kind:
  - apply a fixture;
  - re-apply (no-op);
  - apply an older event (ignored).

  A rebuild test compares the incremental projections with the rebuilt
  ones over a recorded log, `internal/store/testdata/log.jsonl`.

#### Success Criteria

- Every catalogue kind has an apply and its three tests.
- The rebuild equals the incremental result.

### Phase 4: purl, report dependencies and the dashboard parser

#### Tasks

- [ ] 4.1 `internal/purl`: `FromRenovate(datasource, packageName,
  version string) (string, bool)` with DESIGN-0004's table. It
  percent-encodes the namespace (`@types/node`), handles docker library
  images and digests, and returns `false` when there is no type. Unit
  table tests.
- [ ] 4.2 `internal/renovate/report.go`: decode `packageFiles[].deps[]`
  (`depName`, `packageName`, `datasource`, `currentValue`,
  `currentVersion`, `depType`, `skipReason`). `RunResult.Dependencies`
  carries them, capped (OQ5). Refresh the testdata from a real report
  with deps.
- [ ] 4.3 `internal/renovate/dashboard`: `Parse(body string) (Snapshot,
  error)`.
  - Sections are keyed by machine marker (`approve-branch`,
    `unschedule-branch`, `unlimit-branch`, `retry-branch`,
    `rebase-branch`, `recreate-branch`, and others, with `other` for
    unknown ones).
  - Each item records its branch, title, dependencies and ticked state.
  - `Diff(before, after Snapshot) []Tick` finds what a human ticked.
- [ ] 4.4 Testdata: the proxmox-go-sdk#34 bodies from before and after
  the pin fix, a body with every section, an unknown marker, and a bot
  rewrite versus a human tick.
- [ ] 4.5 `boopd_dashboard_parse_total{outcome}`.

#### Success Criteria

- `go test ./internal/purl/... ./internal/renovate/...` passes.
- The proxmox-go-sdk bodies parse into the expected sections, with the
  pin branch under Errored before the fix and Open after it.

### Phase 5: Worker writers

#### Tasks

- [ ] 5.1 `internal/config`: the `store` block (`enabled`, the four DSN
  secret refs: owner, worker, ingest, ro; `retention`), validation, and
  `docs/config.md` rows.
- [ ] 5.2 The worker builds a pgx pool from the worker DSN when
  `store.enabled`, checks `RequireVersion`, and closes the pool on stop.
- [ ] 5.3 `RecordRun` activity. It maps `RunResult` to `run.started` and
  `run.finished` (runs, branch_updates with purls, dependencies with
  purls, the repository state). The key is `run:<run id>:<attempt>`, and
  the cause is the `run-scheduled` event key.
- [ ] 5.4 `RecordEvents` activity for workflow decisions. The key is
  `wf:<workflow id>:<run id>:<seq>`, where `seq` is a workflow-local
  counter carried through ContinueAsNew.
- [ ] 5.5 `storeOptions` (unlimited retry, 10 s start-to-close, low
  priority) in `internal/workflows/options.go`.
- [ ] 5.6 `RepoWorkflow`: call `RecordRun` after the outcome decision,
  and `RecordEvents` at IMPL-0004's decision points (onboarded,
  offboarded, refreshed, run-scheduled, stalled, capacity-denied,
  budget-wait). Both are gated by `store.enabled`, which `PlanRun`
  returns as `RunPlan.Record`, so a disabled store adds no activities.
- [ ] 5.7 The `recheck` payload: `Recheck{Actor, RequestID}`, optional.
  The workflow decodes it when present (`ReceiveAsync` into a value,
  tolerating an empty payload) and records `action.recheck`. Tests with
  and without a payload (OQ9).
- [ ] 5.8 `DiscoveryWorkflow` records `discovery.finished`, and
  `discovered` signals record `repo.onboarded{via: discovery}` the first
  time.
- [ ] 5.9 Workflow versioning: gate the new activity calls with
  `workflow.GetVersion("store-record", …)`. Extend IMPL-0004's replay
  histories, then replay.
- [ ] 5.10 Tests:
  - activity tests against the test database;
  - workflow testsuite tests for the activity calls and the gating;
  - replay.

#### Success Criteria

- With `store.enabled`, a run through the activity tests leaves runs,
  updates and dependencies rows.
- With it disabled, no store activity is scheduled.
- Replay passes.

### Phase 6: Ingest and the audit into the store

#### Tasks

- [ ] 6.1 `internal/ingest`: `StoreSink` implements IMPL-0004's `Sink`.
  It writes `delivery.received` with the payload subset (OQ7 of
  DESIGN-0004) using the ingest DSN. A failure is logged and counted,
  and never changes the HTTP answer.
- [ ] 6.2 Derived events from the same delivery, appended in the same
  call:
  - `pull_request.*` whose PR author is the bot gives `pr.*`;
  - a bot `issues.edited` on its dashboard gives `dashboard.updated`
    (parsed);
  - a human edit gives `dashboard.ticked` (from the `changes.body.from`
    diff);
  - `installation*` events give `installation.*`.
- [ ] 6.3 `boopd ingest` builds a pool when `store.enabled` and checks
  `RequireVersion`; without the store it keeps `LogSink`.
- [ ] 6.4 Delivery audit: `ListHookDeliveries` appends
  `delivery.failed`, `delivery.redelivered` and `delivery.missed`. A GUID
  with a 2xx at GitHub and no `delivery.received` is missed, and
  `boopd_ingest_deliveries_missed_total` starts counting (IMPL-0004
  OQ6).
- [ ] 6.5 Tests: the handler with `StoreSink` against the test database
  (each derived kind; a store outage still answers 202); the audit's
  missed classification.

#### Success Criteria

- Every routed or recorded delivery type in the testdata produces its
  rows.
- The missed metric moves for an undelivered GUID.

### Phase 7: Pull request reconcile

#### Tasks

- [ ] 7.1 `internal/platform/github`: `SearchOpenBotPRs(ctx, appSlug)`
  as one GraphQL search per installation (`is:pr is:open
  author:app/<slug>`, 100 per page), plus `GetPullRequest(repoID,
  number)` for PRs that left the open set.
- [ ] 7.2 `ReconcilePullRequests` activity, run per installation after
  each discovery pass. It appends `pr.synced` for every open PR and for
  each one that closed or merged since. Its cost goes through the budget
  `ReadRateLimit` already watches.
- [ ] 7.3 `fakegithub`: the search query and the PR GET.
- [ ] 7.4 Tests: reconcile against fakegithub (opened, merged and closed
  while ingest was off); the order guard keeps a newer webhook state.

#### Success Criteria

- With ingest off, the PR projection matches fakegithub after one pass.

### Phase 8: Retention

#### Tasks

- [ ] 8.1 `StoreMaintenanceWorkflow` and its activities. They create
  partitions two months ahead, drop partitions past `retention.events`,
  null `webhook_deliveries.payload` past `retention.delivery_payloads`,
  delete offboarded projections past `retention.offboarded`, and delete
  `runs` past `retention.events`.
- [ ] 8.2 A daily Schedule (`store-maintenance`, overlap `SKIP`) when
  `store.enabled`.
- [ ] 8.3 Tests (integration): each rule, with a clock override.

#### Success Criteria

- A month of fixtures past retention is gone after one run.
- Nothing inside the windows changes.

### Phase 9: The API contract and server

#### Tasks

- [ ] 9.1 `api/openapi.yaml` (OpenAPI 3.1), written by hand:
  - every DESIGN-0004 endpoint, with snake_case fields;
  - open-enum descriptions;
  - the `Problem` schema with `request_id`;
  - cursor parameters, `security` on everything except `/status` and
    `/openapi.yaml`;
  - event kinds as a documented open enum, with per-kind `data` schemas
    (OQ5).
- [ ] 9.2 `api/vacuum.yaml` (camel-case rule off) and `just lint-api`.
- [ ] 9.3 `api/oapi-codegen.yaml` (std-http, strict server, models,
  embedded spec) → `internal/api/gen`, committed and drift-gated.
- [ ] 9.4 Copy into `internal/api`, with provenance headers (OQ6):
  - `problem.go`, `cursor.go`, `middleware.go` (request ID, access log,
    recover);
  - `authn.go` (go-oidc, ID tokens refused, background discovery,
    `boopd_api_auth_failures_total`);
  - the contract-test helper `apitest`.

  Adapt names to boop, and keep no import of repo-guardian.
- [ ] 9.5 `internal/api/authz.go`: the authz file
  (`owners`/`operators`/`clients`), visibility resolved per request, and
  SQL filtering helpers. A missing owner returns 404; a non-operator
  action returns 403.
- [ ] 9.6 `boopd api --config <file>`: the read-only pool, Temporal
  client, OIDC verifier, health and readiness (store version, issuer
  discovered), metrics, and graceful stop.
- [ ] 9.7 Contract tests: every handler's response is validated against
  the embedded spec, with a cursor-binding test and the authn and authz
  matrix.

#### Success Criteria

- `just lint-api` and the drift gates pass.
- The authn and authz matrix tests pass.
- `boopd api` serves `/api/v1/openapi.yaml`.

### Phase 10: API endpoints, recheck, status and estate

#### Tasks

- [ ] 10.1 Read endpoints over sqlc queries:
  - `/me`, `/installations`, `/repositories` (+ `/{id}`, `/events`,
    `/runs`, `/dependencies`);
  - `/pull-requests` (+ detail), `/dashboard-items`, `/packages`,
    `/dependencies`, `/updates`;
  - `/runs`, `/events`, `/deliveries`.

  Each runs in a read-only transaction with a 5 s statement timeout.
- [ ] 10.2 `/repositories/{id}` adds the Temporal `state` Query (a 2 s
  deadline, falling back to stored state with `live: false`).
- [ ] 10.3 `POST /repositories/{id}/recheck` (operators):
  - it signals `recheck{actor, request_id}` and answers 202;
  - 409 `workflow_missing` when there is no workflow;
  - 503 when Temporal is unreachable;
  - a per-replica dedupe of one per repository per minute.
- [ ] 10.4 `/estate` and `/estate/series`, computed on read and cached
  60 s per (scope, range, metric). The p95 is logged per query for the
  rollup decision.
- [ ] 10.5 `/status`: computed every 30 s into a cache (components,
  fleet numbers, version), with `TestStatus_PrivacyGuard` (no free-text
  fields outside an allowlist).
- [ ] 10.6 Tests (integration):
  - each endpoint over seeded data;
  - visibility filtering;
  - recheck's responses against the dev server;
  - estate numbers against a hand-computed fixture.

#### Success Criteria

- Every endpoint is contract-valid over the seed.
- A recheck from the API is recorded as `action.recheck` with the actor
  (through Phase 5.7).

### Phase 11: The UI scaffold, copied

#### Tasks

- [ ] 11.1 Copy repo-guardian v2 @ `a72faf9` `ui/` scaffolding into
  `ui/`:
  - `server/`: config, OIDC, sessions, proxy, static, health, and the
    mock issuer;
  - `web/` shell and primitives, `index.css` tokens, fonts, icons;
  - `scripts/` (`check-api`, `check-dist`, `visual`);
  - test setup, ESLint config, `tsconfig`, `vite.config.ts`.

  Every copied file starts with a provenance header, and `ui/COPIED.md`
  lists source path, commit and local changes (OQ6). Nothing references
  repo-guardian at build or run time.
- [ ] 11.2 Rename and adapt:
  - the cookie becomes `__Host-boop_session`, the client `boopd-ui`;
  - brand: the boop mark, "boop", "renovate · fleet";
  - the `NAV` groups FLEET / ACTIVITY / SYSTEM;
  - no app switcher;
  - the proxy allowlist: GET and HEAD plus `POST
    /api/v1/repositories/*/recheck` with an Origin check.
- [ ] 11.3 Badge tone table for boop's states (DESIGN-0004 § Visual
  design); the open-enum fallback to mute.
- [ ] 11.4 Generated types from `api/openapi.yaml` (`generate-ui-api`)
  and the drift check.
- [ ] 11.5 `ui/Dockerfile` (Bun build, deps, distroless) and the
  `docker-bake.hcl` targets `ui-dev`, `ui-ci` and `ui-release`, with the
  same tag as `boopd`.
- [ ] 11.6 `ui/design/boop-mockup.html`, copied from repo-guardian's
  mockup and restyled for boop's pages.
- [ ] 11.7 Tests: `bun test` (server and primitives), the CSP and
  `check:dist` gates, and the ESLint ban on HTML sinks.

#### Success Criteria

- `just ui-lint ui-test ui-build` passes.
- The image builds, and the shell renders against the mock issuer.
- `grep -r repo-guardian ui/` matches only provenance headers and
  `COPIED.md`.

### Phase 12: UI pages

#### Tasks

- [ ] 12.1 Estate (`/`): KPI strip, the open-PR trend, PR age buckets,
  runs by outcome, top pending packages, ecosystems, and the
  installations table.
- [ ] 12.2 Pull requests (`/pulls`): a DataTable with filter chips, sorted
  oldest first, and a drawer with updates (package, from → to, purl),
  the timeline and a GitHub link.
- [ ] 12.3 Waiting (`/waiting`): items grouped by section, with waiting
  since, ticked by and a dashboard link.
- [ ] 12.4 Repository (`/repositories/$id`): a header with the live
  phase and Recheck (operators); tabs for Timeline (with causes), Runs,
  Pull requests, Waiting, Dependencies and Config.
- [ ] 12.5 Repositories, Packages (+ `$purl`), Runs, Events (15 s
  polling), Deliveries, and Status (public, outside the shell, 30 s
  refetch).
- [ ] 12.6 Polling (DESIGN-0004 OQ10): 30 s for lists, and 5 s on a
  repository with a run in progress.
- [ ] 12.7 Tests:
  - component tests per page;
  - Playwright flows (login, each page, the recheck as operator and as
    viewer, an invisible owner gets 404);
  - axe on every page;
  - screenshots in the pinned Playwright image.

#### Success Criteria

- Every page renders over the seed with no axe violations.
- The Playwright suite passes, and screenshots are committed.

### Phase 13: Chart

#### Tasks

- [ ] 13.1 Values and schema:
  - `store` (`enabled`, `existingSecret` with the four DSN keys,
    `migrate` hook style `helm` or `argocd`);
  - `api` (Deployment, replicas, PDB, Service, authz, `ingress` and
    `httpRoute`);
  - `ui` (Deployment, PDB, Service, `existingSecret`, `publicURL`,
    `ingress` and `httpRoute`);
  - `boopd.store` and `boopd.api` rendered into `boopd.hcl`.
- [ ] 13.2 `templates/migrate-job.yaml`, a hook Job with the owner DSN
  only, waiting before the roles start.
- [ ] 13.3 `templates/api.yaml`, `api-authz-configmap.yaml` (checksum
  annotation), `ui.yaml`, and the routes for both.
- [ ] 13.4 NetworkPolicies:
  - ui → api;
  - api → Postgres `-ro` and Temporal;
  - worker and ingest → Postgres `-rw`;
  - migrate → `-rw`;
  - ui → IdP;
  - api → IdP (JWKS).
- [ ] 13.5 Guards: `ui.enabled` requires `api.enabled` and auth; the
  roles with `store.enabled` require `store.existingSecret`; a UI route
  without auth fails.
- [ ] 13.6 helm-unittest suites `store_test.yaml`, `api_test.yaml`,
  `ui_test.yaml`:
  - the hooks;
  - each role mounts only its own DSN;
  - the ui has no ServiceAccount token;
  - the guards and the NetworkPolicies.
- [ ] 13.7 `just helm-docs`, and the rendered-config validate.

#### Success Criteria

- `just helm-test` passes.
- With every new value off, existing resources render byte-identical.

### Phase 14: End to end

#### Tasks

- [ ] 14.1 e2e Postgres (OQ10): a pinned `postgres:17` Deployment in the
  e2e namespace, with an init script creating the four roles. The chart
  installs with `store`, `api`, `ui` and `ingest` enabled, and the mock
  OIDC issuer runs as a pod.
- [ ] 14.2 Scenarios (`test/e2e/store_test.go`, `api_test.go`):
  - `TestStore_RunRecorded`: a run gives `run.finished`,
    `branch_updates` and `dependencies` with purls;
  - `TestStore_PROpenedByBot`: a signed bot `pull_request.opened` gives a
    PR row;
  - `TestStore_DashboardWaiting`: a bot dashboard rewrite gives items,
    and a human tick gives `dashboard.ticked`;
  - `TestAPI_RecheckAudited`: an operator's recheck gives a run and
    `action.recheck` with the actor;
  - `TestAPI_Visibility`: an invisible owner gets 404;
  - `TestAPI_StatusAggregateOnly`;
  - `TestStore_Rebuild`: rebuilt projections equal the live ones.
- [ ] 14.3 The UI's Playwright suite runs against the k3d stack in CI as
  its own job (OQ11).

#### Success Criteria

- `just e2e` and the UI e2e job pass locally and in CI.

### Phase 15: CI, release and documentation

#### Tasks

- [ ] 15.1 `ci.yml`:
  - a UI job (install, lint, test, build, check-api, check-dist);
  - an API lint job (vacuum, oapi-codegen drift, sqlc vet and drift);
  - Docker for the integration tests;
  - the UI e2e job.
- [ ] 15.2 `release.yml`: build, sign (cosign) and attest (SLSA) the
  `boopd-ui` image with the same tag, and add it to the release notes.
- [ ] 15.3 `docs/api.md`: auth setup (the Keycloak clients
  `boopd-api` and `boopd-ui`, the audience mapper), authz file, the
  endpoint overview, cursors, errors, and the correlator's entry points
  (`/updates`, `/dependencies` by purl).
- [ ] 15.4 `docs/ui.md`: enabling, sessions and key rotation, pages,
  the status page, and security.
- [ ] 15.5 `docs/config.md` rows; CLAUDE.md layout (`internal/store`,
  `internal/api`, `internal/purl`, `ui/`, `api/`) and roles.
- [ ] 15.6 DESIGN-0004 → Implemented; DESIGN-0003's recording note
  → "stored (IMPL-0005)".

#### Success Criteria

- CI is green on the PR, and a dry-run release builds both images.
- The docs pass markdownlint and the completeness test.

### Phase 16: PRs, releases and the homelab

#### Tasks

- [ ] 16.1 PR A, the store and writers (phases 1–8 and their chart
  pieces), `minor`.
- [ ] 16.2 PR B, the API (phases 9–10 and its chart pieces), `minor`.
- [ ] 16.3 PR C, the UI (phases 11–12, the remaining chart, e2e, CI,
  docs), `minor`.
- [ ] 16.4 **Deferred, human required.** Homelab database setup:
  - a CNPG `Cluster` for boopd with managed roles `boopd_owner`,
    `boopd_worker`, `boopd_ingest`, `boopd_ro`;
  - the DSN Secret;
  - `store.enabled`;
  - the migrate hook runs under Argo CD.
- [ ] 16.5 **Deferred, human required.** Homelab API and UI:
  - Keycloak clients `boopd-api` (audience) and `boopd-ui` (confidential,
    code + PKCE);
  - the authz file;
  - the UI's Tailscale ingress;
  - a walk through every page against the real fleet (proxmox-go-sdk
    first).
- [ ] 16.6 IMPL status → Completed, with the evidence.

#### Success Criteria

- All three releases are out and enabled on fartlab.
- The Estate and Pull requests pages match GitHub for proxmox-go-sdk.
- A recheck from the UI appears in the repository's timeline with the
  actor.

## File Changes

| Path | Phase | Change |
| - | - | - |
| `go.mod`, `mise.toml`, `justfile` | 1 | modules, tools, recipes |
| `internal/store/storetest/*` | 1 | test database helper |
| `internal/store/migrations/*.sql`, `migrate.go` | 2 | schema, roles, goose |
| `internal/store/{event,append,apply_*,rebuild,retention}.go`, `queries/*.sql`, `db/*` | 3, 8 | append, applies, sqlc, rebuild, retention |
| `internal/purl/*` | 4 | datasource → purl |
| `internal/renovate/report.go`, `dashboard/*` | 4 | deps, dashboard parser |
| `internal/config/*`, `docs/config.md`, `examples/boopd.hcl` | 5, 15 | `store` and `api` blocks |
| `internal/activities/{record,reconcile,maintenance}.go` | 5, 7, 8 | store activities |
| `internal/workflows/{repo,discovery,options,maintenance}.go` | 5, 8 | record calls, `recheck` payload, maintenance |
| `internal/ingest/store_sink.go` | 6 | `StoreSink` |
| `internal/platform/github/{search,pulls}.go` | 7 | reconcile queries |
| `test/fakegithub/fakegithub.go` | 7 | search, PR GET |
| `api/{openapi,oapi-codegen,vacuum}.yaml`, `internal/api/**` | 9, 10 | contract, server, handlers |
| `cmd/boopd/{migrate,api,store}.go` | 2, 3, 9 | commands |
| `ui/**`, `docker-bake.hcl` | 11, 12 | BFF, SPA, image |
| `charts/boopd/**` | 13 | store, api, ui |
| `test/e2e/{store,api}_test.go` | 14 | scenarios |
| `.github/workflows/{ci,release}.yml` | 15 | jobs, image |
| `docs/{api,ui}.md`, `CLAUDE.md`, ADR-0006, DESIGN-0003/0004 | 1, 15 | docs, status |

## Testing Plan

- **k3d e2e is the main gate:** runs, PR events, the dashboard, recheck
  auditing, visibility, the status page and rebuild, against the real
  chart, Postgres, the dev server and the mock issuer (Phase 14).
- **The UI e2e** (Playwright) runs against that stack, with axe and
  screenshots.
- **Integration tests on a real Postgres** (Phase 1.4) cover migrations,
  grants, every apply's three cases, rebuild equality, retention and
  the API endpoints. Store behaviour is never mocked.
- **Contract tests** validate every API response against
  `openapi.yaml`.
- **Unit tests** cover pure code: purl, the dashboard parser and diff,
  authz resolution, cursors and the BFF.
- **Replay tests** (from IMPL-0004) grow with the store calls.

## Dependencies

- **IMPL-0004 merged** (the ingest sink, the audit, the decision
  points).
- **New Go modules:** pgx/v5, goose/v3, go-oidc/v3, kin-openapi. **New
  tools:** oapi-codegen v2.8.0 and sqlc (as `go tool`), bun 1.3.14 and
  vacuum (mise), and the Playwright image.
- **Copy source:** repo-guardian v2 @ `a72faf9`, read-only. Nothing
  depends on it after the copy.
- **Homelab:** CNPG, Keycloak and the Tailscale ingress (Phase 16).

## Open Questions

None open. The answered questions are listed under
[Resolved Questions](#resolved-questions).

## Resolved Questions

Answered 2026-10-18: option a for every question.

| OQ | Question | Decision |
| - | - | - |
| 1 | PR split? | **a**: three PRs: the store and writers, the API, the UI |
| 2 | Postgres for integration tests? | **a**: a pinned `postgres:17` container through Docker (`internal/store/storetest`) |
| 3 | Partitions? | **a**: native partitioning with `ensure_partitions`/`drop_partitions` from `StoreMaintenanceWorkflow` |
| 4 | oapi-codegen and sqlc pins? | **a**: `go tool` dependencies in `go.mod` |
| 5 | Dependency list size? | **a**: at most 2,000 in `RunResult.Dependencies`; past that, `RunRenovate` writes them straight to the store, or truncates with a flag |
| 6 | Copied-code marking? | **a**: a provenance header per file plus `ui/COPIED.md` |
| 7 | Idempotency key shape? | **a**: readable text keys built by the writer |
| 8 | Who creates database roles? | **a**: the operator (CNPG managed roles; the e2e init script) |
| 9 | How does `recheck` carry the actor? | **a**: an optional `Recheck{Actor, RequestID}` payload on the same signal |
| 10 | Postgres in k3d e2e? | **a**: a pinned `postgres:17` Deployment with an init script |
| 11 | Where does Playwright run? | **a**: a separate CI job against the k3d stack |
| 12 | UI seed? | **a**: a recorded event log loaded through `boopd store rebuild` |
| 13 | Owner scope? | **a**: one `?owner=` scope across every page and endpoint |
| 14 | Status page on the internet? | **a**: not now; unauthenticated on the tailnet-only UI host |
| 15 | Store disabled? | **a**: `PlanRun` returns `Record=false`, and no store activities are scheduled |

## References

- DESIGN-0004 (resolved OQs), DESIGN-0003 / IMPL-0004
- ADR-0006, ADR-0007, ADR-0008
- repo-guardian v2 @ `a72faf9` (the copy source): DESIGN-0027, DESIGN-0036,
  IMPL-0031, `ui/`, `internal/api`, `internal/store/postgres`
- [pgx](https://github.com/jackc/pgx), [goose](https://github.com/pressly/goose),
  [sqlc](https://sqlc.dev), [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen),
  [go-oidc](https://github.com/coreos/go-oidc)
- [PostgreSQL table partitioning](https://www.postgresql.org/docs/current/ddl-partitioning.html)
- [Package URL specification](https://github.com/package-url/purl-spec)
