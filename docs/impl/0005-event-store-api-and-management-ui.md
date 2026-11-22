---
id: IMPL-0005
title: "Event store, API and management UI"
status: Draft
author: Donald Gifford
created: 2026-10-18
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0005: Event store, API and management UI

> **Amended 2026-11-21 (INV-0005).** The UI is built on
> `@donaldgifford/design-system`, with repo-guardian's `ui/` as the
> example and nothing copied from it. Phases 11 and 12 are rewritten, and
> the objective, scope, Phases 13 and 15, dependencies and OQ6 are
> updated. Phases 1 to 10 are unchanged; Go code is still copied with a
> provenance header (Phase 9.4).

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Findings (2026-10-18)](#findings-2026-10-18)
- [Findings (2026-11-21)](#findings-2026-11-21)
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
  - [Phase 11: The UI scaffold on the design system](#phase-11-the-ui-scaffold-on-the-design-system)
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

Build DESIGN-0004 (all OQs resolved; amended 2026-11-21 by INV-0005):

- **A Postgres event log** (`events`, append-only), with projections
  applied in the same transaction and rebuildable by replay. The
  projections are repositories, runs, proposed updates, current
  dependencies, PRs, dashboard items, deliveries and discovery passes.
- **Writers:** the worker (through activities), ingest (directly, with
  its own role) and the delivery audit. The API is read-only.
- **`/api/v1`:** OpenAPI 3.1 with OIDC bearer authentication, owner
  visibility plus an `operators` list, keyset cursors, RFC 9457 errors,
  one action (recheck), a public aggregate status, and estate numbers.
- **`boopd-ui`:** a Bun + Hono BFF and React 19 SPA in `ui/`, built on
  `@donaldgifford/design-system` with repo-guardian v2's stack and
  versions. It has the Estate, Pull requests, Waiting, Packages,
  Repositories, Repository, Runs, Events, Deliveries and Status pages.
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
- `ui/`: the BFF, the SPA, tests, the Dockerfile, and the registry
  configuration (`.npmrc`).
- `docker-bake.hcl`, `charts/boopd`, `.github/workflows`, `justfile`,
  `mise.toml`, `renovate.json5`.
- Docs: `docs/config.md`, `docs/api.md`, `docs/ui.md`, and notes on
  ADR-0006 and DESIGN-0003.

### Out of Scope

- **The security correlator and `findings`** (a later design). The
  `purl` columns and the `/updates`, `/dependencies` and `/packages`
  filters are the hand-off.
- **Redeliver and approve actions,** and evals.
- **The design system's own changes.** Generic components that boop
  needs are built in `donaldgifford/design-system` (Phase 12.7 tracks
  them); their design and release are that repository's.
- **A combined UI and the app switcher** (DESIGN-0004 non-goals;
  INV-0005 OQ8).
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
- **repo-guardian v2 @ `a72faf9`** is the copy source (for the UI this is
  superseded; see [Findings (2026-11-21)](#findings-2026-11-21)). It is not on
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

## Findings (2026-11-21)

From INV-0005, which replaces the UI part of the finding above:

- **The design system exists.** `@donaldgifford/design-system` 0.3.2 is
  on GitHub Packages, and repo-guardian's `ui/` runs on it (`main` @
  `cc97e3f`). The primitives, `index.css` tokens and fonts that Phase 11
  planned to copy were deleted there.
- **`a72faf9` is on repo-guardian's `main` now.** It stays the copy
  source for the Go code in Phase 9.4. Nothing in `ui/` is copied.
- **The toolchain to match** is repo-guardian's `ui/` (INV-0005 finding
  7): Bun 1.3.14 and no Node, TypeScript `^5.9` with its strict flags,
  React `^19.3`, Vite `^8.3`, Tailwind `^4.3`, TanStack Router `^1.170`
  and Query `^5`, Hono `^4.13`, `openid-client` `^6.8`, `jose` `^6.2`,
  openapi-typescript `^7.13` and openapi-fetch `^0.17`, Tabler icons,
  ESLint `^10`, happy-dom `^21`, Playwright `^1.63`. Take the versions
  from its `ui/package.json` on the day `ui/` is scaffolded.
- **The package ships source.** boop's Vite and `tsc` compile it, so the
  compiler flags matter, and the test preload must register the DOM
  before Base UI is imported.
- **GitHub Packages needs a token to read,** even for a public package.
  Four install paths need it: a laptop, CI, the image build and
  Renovate. boop has no npm tooling in CI or Renovate today.
- **The BFF has no example in the package.** It is written here, with
  repo-guardian's server tests ported (INV-0005 OQ6).
- **Known component gaps** (INV-0005 finding 8): stacked bars over time
  for "runs by outcome", a segmented control for the range, popover
  option rows for the owner scope, a filter-chip bar, and one-line
  loading and empty text. They are built in the design system first
  (INV-0005 OQ7), which needs its RFC-0001 entry rule amended.

Later the same day, after the mockup:

- **`ui/` is no longer empty.** A mockup runs over fixture data
  (`just ui-dev`): the shell with the app switcher, Estate, Pull
  requests, one repository page and the public status page. It has no
  server, no API client, no sign-in and no tests.
- **design-system 0.4.0 carries every known gap:** the segmented
  control, option rows, filter bar, inline loading and empty text,
  stacked bars over time, the app switcher (its RFC-0002) and the status
  page parts. RFC-0001's entry rule is amended. `ui/` pins 0.4.0 and
  holds no local copy.
- **Two prop gaps are filed there,** not patched here: `Meter` reads a
  high percentage as good (design-system issue 29), and `Timeline` takes
  its time as a string (issue 30). The mockup works around both.
- **Vite's dev server needs the package's CommonJS dependencies named**
  in `optimizeDeps.include`, because the package ships source
  (`ui/web/vite.config.ts`).

## Findings (Phase 2)

Choices made while writing the schema, where the design left the detail
open:

- **The idempotency key is its own table.** Postgres requires a unique
  constraint on a partitioned table to include the partition key, so
  `events.key` cannot be unique across months. `event_keys(key,
  occurred_at)` is unpartitioned; `Append` inserts the key first in the
  same transaction and skips the event when it exists. `drop_partitions`
  prunes old keys with their partitions.
- **`events` has a default partition** for anything outside the monthly
  ones, such as a replayed log older than the first partition.
  `drop_partitions` deletes its old rows too.
- **Projections have no foreign keys.** They are rebuilt from events, and
  an apply must not fail because a repository's events arrived before its
  installation's.
- **Ingest may DELETE `dashboard_items`.** `dashboard.updated` replaces a
  repository's items, which the role table in DESIGN-0004 did not list.
- **`boopd_ro`'s parameters** (`default_transaction_read_only`, a 5 s
  `statement_timeout`) are set by `0003_grants.sql` when the owner may
  alter roles, and warned about otherwise. The API's pool sets both on
  every connection (9.6), so a managed owner without `CREATEROLE` is
  covered.
- **`store_meta.schema_version`** is what `RequireVersion` reads, since
  every role can read it and only the owner can read goose's table.

## Findings (Phase 3)

- **Kinds with no projection** (`pr.checkbox`, `action.recheck`) have no
  apply. Their events are recorded, and their tests check that no
  projection changes.
- **Repository state names.** `run.stalled` sets `stalled`,
  `run.capacity-denied` sets `waiting`, and `run.budget-wait` sets
  `rate-limited`. A `run.finished` that `Failed` or `TimedOut` sets
  `errored`; any other outcome sets `idle`.
- **A run seen before its repository's onboarding** creates a placeholder
  row whose lifecycle guard is `-infinity`, so the onboarding still
  applies when it arrives.
- **Arrival order.** Rebuild equals the incremental result for the
  recorded log, which delivers PR, delivery, run, installation and
  dashboard-tick events out of order. Two orders still differ:
  - a tick that arrives before the rewrite that first lists its item is
    dropped incrementally;
  - an offboarding that arrives before its onboarding leaves
    `onboarded_at` empty.

  GitHub and the worker do not produce either today. Run `boopd store
  rebuild` if one is ever seen.
- **Rebuild is one transaction as the owner.** Writers block on the
  truncated tables until it commits, then apply on top, so no event is
  lost or applied twice.

## Findings (Phase 4)

- **purl details the design's table left open:**
  - Docker images outside Docker Hub carry their registry as
    `repository_url`.
  - A digest version makes an `oci` purl whose `repository_url` is the
    full image path.
  - A GitHub Action at a path (`actions/cache/save`) keeps the path as
    the subpath.
  - PyPI names are normalised (lower case, `_` to `-`).
  - The `run.finished` apply computes `purl_from`, `purl_to` and
    `dependencies.purl` at write time. A dependency's purl uses
    `currentVersion`, so a range with no resolved version has none.
- **The real Renovate 44 live fixture already has deps** (140, from a
  run against a scratch repository). The golden pins them, so there was
  nothing to re-record. `PackageName` falls back to `depName`, which
  Renovate omits when the two match.
- **The dashboard's last box follows a rule.** Renovate writes
  `<!-- manual job -->` after a `---` that follows Detected Dependencies.
  The parser ends a section at a rule, so that box has no section.
- **Global boxes** (`rebase-all-open-prs`, `create-all-rate-limited-prs`,
  `create-all-awaiting-schedule-prs`, `approve-all-pending-prs`, `manual
  job`, `create-config-migration-pr`) are items with no branch, so a
  tick on one is found by `Diff` like any other.
- **The proxmox-go-sdk#34 fixtures come from the issue's edit history**
  (GraphQL `userContentEdits`). `internal/renovate/dashboard/testdata/README.md`
  records the query and which revision is which.

## Findings (Phase 5)

- **Recording is off the critical path.** `RepoWorkflow` starts
  `RecordRun` and `RecordEvents` without waiting for them. It waits for
  any still in flight only before it ends or continues as new. Both
  retry without limit at low priority (`storeOptions`), so a store outage
  delays the next ContinueAsNew, not a run.
- **Identifiers:**
  - The `runs` id is `<workflow run id>:<attempt>`. The run's events are
    `run:<run id>:started` and `run:<run id>:finished`.
  - Decision keys are `wf:<workflow id>:<run id>:<seq>`, with
    `RepoState.RecordSeq` carried through ContinueAsNew.
  - `RepoState.CauseKey` is the last `run-scheduled` or `action.recheck`
    key, and it becomes the next run's `cause_key`.
- **What is recorded where:**
  - `repo.run-scheduled` is recorded for the first due time, each cadence
    decision and each webhook event.
  - `run.budget-wait` and `run.capacity-denied` are recorded once per
    acquire, not on every retry.
  - `run.stalled` is recorded when the stall limit is reached.
- **Gating.** `GetVersion("store-record")` plus `RunPlan.Record` for
  `RepoWorkflow`, and plus `DiscoveryInput.Record` for
  `DiscoveryWorkflow`. The discovery Schedule's input carries `Record`
  from `store.enabled`, and the worker updates the Schedule at start.
- **Workflows do not import `internal/store`** (a depguard rule). They
  carry the kind strings and plain record types, and a test keeps the
  kinds in the store's catalogue. The `delivery` cause of a webhook
  decision waits for Phase 6's delivery keys.
- **Replay.** The histories recorded before the gate are kept as
  `*-pre-store.json`. The re-recorded set carries the gate's marker, and
  two new histories record to the store.
- **The append outcome label is `applied`**, as in DESIGN-0004's metrics
  table, not `appended`.

## Findings (Phase 6)

- **StoreSink runs after the answer is written** and before the handler
  returns, so GitHub sees the 2xx only after ingest has recorded the
  delivery. That is what lets the audit call a listed 2xx without a
  `delivery.received` missed, without a grace period. A store failure is
  logged and counted by `boopd_store_append_total{outcome="error"}`. It
  never changes the answer. The append is bounded by
  `StoreAppendTimeout` (5 s).
- **Keys:**
  - `delivery.received` is
    `delivery:<guid>:received:<received at, RFC3339Nano>`, so a
    redelivery of the same GUID is a new event.
  - Derived events are `delivery:<guid>:<kind>[:i]`, with the received
    key as their cause.
  - Audit events are `audit:<guid>:<newest GitHub attempt id>:<kind>`.
    A lost cursor re-reads to duplicates, and a later attempt is new.
- **The audit writes attempt 1**, the row `delivery.received` writes,
  so GitHub's status and ingest's outcome share one `webhook_deliveries`
  row per GUID. Delivery IDs that are not UUIDs (only tests send them)
  are not recorded.
- **Missed takes precedence over redelivered.** A 2xx redelivery that
  ingest has no record of is still missed.
- **`boopd ingest` opens the store before dialling Temporal**, so a
  missing DSN or an unmigrated schema fails at start.
- **`docs/config.md`'s `store` example was not valid HCL.** It used
  one-line blocks with two attributes, which HCL rejects, and is now
  spelled out. No test decodes that example; `examples/boopd.hcl` only
  mentions the block in a comment.
- **Open from Phase 5:** a webhook decision's `cause_key` is still the
  workflow's own key. The received key carries the receipt time, which
  the workflow does not know. `Changed.DeliveryID` reaches the workflow,
  so the API can join a decision to its delivery on `delivery_id`
  instead.

## Findings (Phase 7)

- **The search is scoped to the installation's account.** An
  installation token's search also covers public repositories elsewhere.
  The query is `is:pr is:open author:app/<slug> user:<account>`, and the
  account comes from `ListInstallations`. The slug is the bot login
  without `[bot]`: `webhook.bot` when set, else `<app name>[bot]`.
- **What left the open set:** the PRs the store holds open in the
  installation's repositories (`OpenPullRequests`, joined on
  `repositories.installation_id`) that the search did not list. A PR or
  repository that answers 404 is skipped. Offboarding removes the
  repository's rows.
- **`pr.synced` keys** are
  `reconcile:<repo>:<number>:<updated_at>:<state>`. A second pass over
  unchanged PRs appends only duplicates, and `apply_pr`'s guard on
  `gh_updated_at` keeps a newer webhook state over a stale search
  answer.
- **Discovery runs it after the installations' passes** and only when
  recording, behind `GetVersion("pr-reconcile")`. It runs at low
  priority with a 30-minute StartToClose, a heartbeat on every read and
  three attempts. A failure counts in `DiscoveryResult.ReconcileFailed`.
  The recording discovery history from Phase 5 is kept as
  `discovery-store-record-pre-reconcile.json`.
- GitHub's search returns at most 1000 results, so a fleet with more open
  bot PRs than that on one account syncs only the first 1000 per pass.

## Findings (Phase 8)

- **The events cutoff is rounded down to its month.** `drop_partitions`
  deletes idempotency keys and default-partition rows older than the
  time it is given. With the exact cutoff, a partition that stays would
  lose its events' keys. Rounding to the month keeps events, keys and
  the default partition expiring together, a month at a time.
- **The offboarded rule needed a grant.** Migration 0004 gives
  `boopd_worker` DELETE on `dashboard_items` and `dashboards`, which
  ingest otherwise owns. `SchemaVersion` is now 4, so a worker or ingest
  from this build refuses to start until `boopd migrate` has run.
- **One activity, one rule.** `StoreMaintenanceWorkflow` runs
  `MaintainStore` for each rule in turn, at low priority with three
  attempts. A failed rule is counted, and the next day's pass catches
  up. The rule names live in both packages (depguard), and a test keeps
  them in step.
- **The clock override is the workflow's start time**, passed as
  `MaintainStoreInput.Now`. `ensure_partitions` still uses the
  database's clock, which is right for creating months ahead.
- A retention of `0` keeps everything for that rule. Partitions are
  still created.

## Findings (Phase 9)

- **The scope is a store type.** `store.Scope{All, Owners}` is what every
  API read takes. `api.Scope` resolves it from the authz file and hands
  it over with `Store()`, which always sends an owners array (never
  NULL) for `@owners::text[]`.
- **The operator gate is by method.** A strict middleware refuses any
  non-GET operation to a caller without an operator group, with 403. It
  runs after the visibility gate, so a caller who sees nothing hears
  that first. The authz-disabled anonymous principal is no operator.
- **Public routes carry no principal.** `/openapi.yaml` and a public
  `/status` skip authn and authz entirely, so a handler there cannot
  read a scope by mistake.
- **The issuer may be http on loopback only**, for local and test
  issuers. Anywhere else it must be https.
- **Temporal is dialled lazily.** The API starts and serves reads while
  Temporal is away, and Temporal is not a readiness check. Recheck
  answers 503 when the call fails (Phase 10). Readiness is the store's
  schema version plus OIDC discovery.
- **The read-only pool enforces itself.** Each connection starts with
  `default_transaction_read_only = on` and `statement_timeout = 5s`, so
  even a DSN for a role with more grants cannot write through it
  (TestOpenReadOnly).
- **`/me` found a nil-slice bug.** With no installations the store
  returns nil, which marshals to `null` against a required array. The
  handler sends `[]`; Phase 10's lists need the same care.
- **Refused requests are labelled `unmatched`.** Authentication runs
  before the inner mux matches, so a 401 or 503 carries
  `route="unmatched"` on `boopd_api_requests_total`. This keeps the
  label set bounded under scans, and the auth-failure counter carries
  the reason.
- **Cursor binding is unit-tested for now** (`cursor_test.go`). The
  HTTP-level replay test lands with the first list handler in Phase 10.
- Every operation not yet implemented answers a typed 501
  (`not_implemented`), which the spec's `default` response admits.

## Findings (Phase 10)

- **Migration 0005 bumps the schema to 5.** It adds the `run_cause()`
  SQL function and an `events(key)` index; 0001 already indexes
  `cause_key`. The worker and ingest refuse to start below the version
  they expect, so a deploy runs `boopd migrate` first, as with every
  migration.
- **A run's cause is derived, not stored.** `run_cause(cause_key)` looks
  up the event the key names:
  - `action.recheck` gives recheck;
  - `repo.run-scheduled` with a reason, a `delivery:` key or a webhook
    source gives webhook;
  - `repo.run-scheduled` without a reason gives schedule;
  - anything else, including no key, gives unknown.
- **The live view needs no new workflow commands.** The `state` Query
  returns `RepoView`: `RepoState` plus phase, running-since and the
  budget wait. It is replay-safe, and `waiting-budget` exists only in the
  Query, never as a search attribute.
- **Lists read limit+1 rows.** The extra row decides `next_cursor`, and
  the cursor's filter hash covers every query parameter except `cursor`
  and `limit`, plus the path id for per-repository lists. Replaying a
  cursor under another filter answers 400. The integration test now
  covers this over HTTP.
- **sqlc constraints shaped the queries.** Two-argument `unnest`, a
  VALUES column alias and `FILTER` with casts all fail sqlc's rewrite.
  The batch lookups use one `unnest` per column, and the estate counts
  use `sum(CASE …)`. Nullable `min`/`max` arrive as `interface{}` and are
  unwrapped by `timeOrNil`.
- **The estate reads in one snapshot.** Its queries share a
  RepeatableRead read-only transaction, so the numbers agree with each
  other. Timings feed the p95 that is logged per query, for the rollup
  decision.
- **`/deliveries` is for callers who see all owners.** A delivery may
  carry no repository, so scoping it would leak or hide arbitrarily; a
  narrower caller gets 403.
- **Status is aggregate-only.** The 30 s page holds:
  - component states;
  - fleet counts;
  - the latest per-installation budget spend;
  - the version.

  `TestStatus_PrivacyGuard` walks the JSON and refuses any string field
  outside the allowlist.

## Findings (Phase 11)

- **The BFF's tests passed as a port.** All 78 behaviours carried over
  with boop's names: the cookie, the client, the recheck POST and
  `UI_LINKS` in place of a docs link.
  - `proxy.test.ts` also holds the forwarded POSTs to the spec's POST
    operations, so a new action fails the test until the proxy allows
    it.
  - The public paths are checked against `security: []` in
    `api/openapi.yaml`.
- **Links are validated at start.** `UI_LINKS` is at most ten https
  links, and `/ui/config` passes them to the sidebar and the command
  palette. Nothing else in the UI links out.
- **The `/me` gate needs no 403.** `/me` is exempt from the visibility
  gate, so a caller with no owners gets 200 with `owners: []` and
  `all_owners: false`. The shell renders the no-access view (with a sign
  out) instead of pages that would all answer 403.
- **Not-found renders inside the frame.** An unmatched path never
  matches the pathless shell route, so it fell through to the root and
  rendered without the sidebar. A `$` splat route under the shell keeps
  it in the frame.
- **`check-tokens` enforces "no colour outside the package"** rather than
  a list of removed names:
  - Tailwind palette colours;
  - arbitrary colour values;
  - colour functions;
  - `--color-*` definitions;
  - hex colours in CSS. Hex is not checked in TSX, where `#123` is a
    pull request number.

  `web/src` passed it unchanged.
- **No token in the image.** The token is a required build secret for the
  build stage only. A scan of every layer, the history and the metadata
  for the token's value found nothing.
- **The image signs in.** The read-only image ran against the mock
  issuer and a stub API sharing its network namespace (so both are on
  loopback, as the config requires). The sign-in redirect, the callback,
  the session cookie, the app under the `'self'` CSP, the proxied `/me`
  and the cached assets all worked (`server/testing/stack.ts`, `just
  ui-dev-stack`). Rendering in a browser is Playwright's, in Phases 12
  and 14.
- **Renovate reads the registry through `{{ secrets.NPM_TOKEN }}`.** The
  runner has to provide that secret; this is noted for the homelab
  setup (16.4).

## Implementation Phases

Each phase ends with `just lint`, `just test` and its own checks green,
with one commit per numbered task. The PR split is in OQ1.

### Phase 1: Decisions, toolchain and the test database

#### Tasks

- [x] 1.1 Status changes: DESIGN-0004 → Approved; ADR-0006 → Accepted,
  with "realised by DESIGN-0004".
- [x] 1.2 Go modules: `github.com/jackc/pgx/v5`,
  `github.com/pressly/goose/v3`, `github.com/coreos/go-oidc/v3`,
  `github.com/getkin/kin-openapi`. Tools as `go tool` in `go.mod`:
  `oapi-codegen` v2.8.0 and `sqlc` (OQ4).
- [x] 1.3 `mise.toml`: pins for `bun` (1.3.14) and `vacuum`. Playwright
  runs from its pinned image, not mise.
- [x] 1.4 Test database (OQ2): `internal/store/storetest` starts a
  pinned `postgres:17` container through Docker for tests under the
  `integration` tag, like `temporaltest` for the dev server. `just
  test-integration` covers it, and CI's integration job gets Docker.
- [x] 1.5 `justfile` recipes: `generate-sql`, `generate-api`, `lint-api`
  (vacuum plus drift), `lint-sql` (sqlc vet plus drift), and `ui-*`
  (install, lint, test, build, e2e, visual).

#### Success Criteria

- `go build ./...` with the new modules.
- `just test-integration` starts and stops the test database.
- `just --list` shows the new recipes.

### Phase 2: Schema, roles and migrate

#### Tasks

- [x] 2.1 `internal/store/migrations/0001_events.sql`:
  - `events`, partitioned by month on `occurred_at`;
  - a unique `key` (OQ7);
  - the indexes from DESIGN-0004;
  - `ensure_partitions(months_ahead int)` and `drop_partitions(before
    timestamptz)` as `SECURITY DEFINER` functions.
- [x] 2.2 `0002_projections.sql`:
  - `installations`, `repositories`, `runs`;
  - `branch_updates`, `dependencies`;
  - `pull_requests`, `dashboard_items`;
  - `webhook_deliveries`, `discovery_passes`;
  - `store_meta(schema_version)`.

  Keys and columns are as in DESIGN-0004 § Tables.
- [x] 2.3 `0003_grants.sql`: grants to the existing roles `boopd_worker`,
  `boopd_ingest` and `boopd_ro` (OQ8). `events` has no UPDATE or DELETE
  for anyone but the owner, and `boopd_ro` has
  `default_transaction_read_only`.
- [x] 2.4 `internal/store/migrate.go`: a goose Provider over the
  embedded files, under an advisory lock, with the version table
  `goose_boopd_version`. `RequireVersion(ctx, pool, n)` makes a role
  refuse to start below the schema it needs.
- [x] 2.5 `boopd migrate --config <file>` in `cmd/boopd`, using the
  owner DSN from `store.owner_dsn_secret_ref` (migrate only).
- [x] 2.6 Tests (integration): up from empty and idempotent re-run;
  role grants (a `boopd_ro` INSERT fails, a `boopd_ingest` UPDATE on
  `events` fails); partition functions.

#### Success Criteria

- `boopd migrate` applies the schema to the test database twice without
  error.
- The grant tests prove that `events` is append-only for every writer
  role.

### Phase 3: Append, applies and rebuild

#### Tasks

- [x] 3.1 `internal/store/event.go`: `Event{Key, OccurredAt, Kind,
  Source, InstallationID, RepoID, PRNumber, Actor, CauseKey, Summary,
  Data}`. `Data` is a typed struct per kind with `V int`, registered in
  a `kinds` table (OQ5).
- [x] 3.2 `internal/store/append.go`: `Append(ctx, events ...Event)`.
  - In one transaction it runs `INSERT … ON CONFLICT (key) DO NOTHING
    RETURNING id`, then the kind's apply for each inserted row.
  - A duplicate skips its apply.
  - It updates `boopd_store_append_total{role,kind,outcome}` and
    `boopd_store_append_seconds`.
- [x] 3.3 sqlc queries in `internal/store/queries/*.sql` and the
  generated package `internal/store/db`. jsonb maps to `json.RawMessage`.
- [x] 3.4 Applies (`internal/store/apply_*.go`), one per kind in the
  DESIGN-0004 catalogue. Each applies the order guard: it compares the
  source timestamp, or `gh_updated_at` for PRs, and ignores older data.
- [x] 3.5 `boopd store rebuild --config <file>`: truncate the
  projections, then stream `events` in `(occurred_at, id)` order through
  the same applies, in batches. The owner DSN is required.
- [x] 3.6 Tests (integration), per kind:
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

- [x] 4.1 `internal/purl`: `FromRenovate(datasource, packageName,
  version string) (string, bool)` with DESIGN-0004's table. It
  percent-encodes the namespace (`@types/node`), handles docker library
  images and digests, and returns `false` when there is no type. Unit
  table tests.
- [x] 4.2 `internal/renovate/report.go`: decode `packageFiles[].deps[]`
  (`depName`, `packageName`, `datasource`, `currentValue`,
  `currentVersion`, `depType`, `skipReason`). `RunResult.Dependencies`
  carries them, capped (OQ5). Refresh the testdata from a real report
  with deps.
- [x] 4.3 `internal/renovate/dashboard`: `Parse(body string) (Snapshot,
  error)`.
  - Sections are keyed by machine marker (`approve-branch`,
    `unschedule-branch`, `unlimit-branch`, `retry-branch`,
    `rebase-branch`, `recreate-branch`, and others, with `other` for
    unknown ones).
  - Each item records its branch, title, dependencies and ticked state.
  - `Diff(before, after Snapshot) []Tick` finds what a human ticked.
- [x] 4.4 Testdata: the proxmox-go-sdk#34 bodies from before and after
  the pin fix, a body with every section, an unknown marker, and a bot
  rewrite versus a human tick.
- [x] 4.5 `boopd_dashboard_parse_total{outcome}`.

#### Success Criteria

- `go test ./internal/purl/... ./internal/renovate/...` passes.
- The proxmox-go-sdk bodies parse into the expected sections, with the
  pin branch under Errored before the fix and Open after it.

### Phase 5: Worker writers

#### Tasks

- [x] 5.1 `internal/config`: the `store` block (`enabled`, the four DSN
  secret refs: owner, worker, ingest, ro; `retention`), validation, and
  `docs/config.md` rows.
- [x] 5.2 The worker builds a pgx pool from the worker DSN when
  `store.enabled`, checks `RequireVersion`, and closes the pool on stop.
- [x] 5.3 `RecordRun` activity. It maps `RunResult` to `run.started` and
  `run.finished` (runs, branch_updates with purls, dependencies with
  purls, the repository state). The key is `run:<run id>:<attempt>`, and
  the cause is the `run-scheduled` event key.
- [x] 5.4 `RecordEvents` activity for workflow decisions. The key is
  `wf:<workflow id>:<run id>:<seq>`, where `seq` is a workflow-local
  counter carried through ContinueAsNew.
- [x] 5.5 `storeOptions` (unlimited retry, 10 s start-to-close, low
  priority) in `internal/workflows/options.go`.
- [x] 5.6 `RepoWorkflow`: call `RecordRun` after the outcome decision,
  and `RecordEvents` at IMPL-0004's decision points (onboarded,
  offboarded, refreshed, run-scheduled, stalled, capacity-denied,
  budget-wait). Both are gated by `store.enabled`, which `PlanRun`
  returns as `RunPlan.Record`, so a disabled store adds no activities.
- [x] 5.7 The `recheck` payload: `Recheck{Actor, RequestID}`, optional.
  The workflow decodes it when present (`ReceiveAsync` into a value,
  tolerating an empty payload) and records `action.recheck`. Tests with
  and without a payload (OQ9).
- [x] 5.8 `DiscoveryWorkflow` records `discovery.finished`, and
  `discovered` signals record `repo.onboarded{via: discovery}` the first
  time.
- [x] 5.9 Workflow versioning: gate the new activity calls with
  `workflow.GetVersion("store-record", …)`. Extend IMPL-0004's replay
  histories, then replay.
- [x] 5.10 Tests:
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

- [x] 6.1 `internal/ingest`: `StoreSink` implements IMPL-0004's `Sink`.
  It writes `delivery.received` with the payload subset (OQ7 of
  DESIGN-0004) using the ingest DSN. A failure is logged and counted,
  and never changes the HTTP answer.
- [x] 6.2 Derived events from the same delivery, appended in the same
  call:
  - `pull_request.*` whose PR author is the bot gives `pr.*`;
  - a bot `issues.edited` on its dashboard gives `dashboard.updated`
    (parsed);
  - a human edit gives `dashboard.ticked` (from the `changes.body.from`
    diff);
  - `installation*` events give `installation.*`.
- [x] 6.3 `boopd ingest` builds a pool when `store.enabled` and checks
  `RequireVersion`; without the store it keeps `LogSink`.
- [x] 6.4 Delivery audit: `ListHookDeliveries` appends
  `delivery.failed`, `delivery.redelivered` and `delivery.missed`. A GUID
  with a 2xx at GitHub and no `delivery.received` is missed, and
  `boopd_ingest_deliveries_missed_total` starts counting (IMPL-0004
  OQ6).
- [x] 6.5 Tests: the handler with `StoreSink` against the test database
  (each derived kind; a store outage still answers 202); the audit's
  missed classification.

#### Success Criteria

- Every routed or recorded delivery type in the testdata produces its
  rows.
- The missed metric moves for an undelivered GUID.

### Phase 7: Pull request reconcile

#### Tasks

- [x] 7.1 `internal/platform/github`: `SearchOpenBotPRs(ctx, appSlug)`
  as one GraphQL search per installation (`is:pr is:open
  author:app/<slug>`, 100 per page), plus `GetPullRequest(repoID,
  number)` for PRs that left the open set.
- [x] 7.2 `ReconcilePullRequests` activity, run per installation after
  each discovery pass. It appends `pr.synced` for every open PR and for
  each one that closed or merged since. Its cost goes through the budget
  `ReadRateLimit` already watches.
- [x] 7.3 `fakegithub`: the search query and the PR GET.
- [x] 7.4 Tests: reconcile against fakegithub (opened, merged and closed
  while ingest was off); the order guard keeps a newer webhook state.

#### Success Criteria

- With ingest off, the PR projection matches fakegithub after one pass.

### Phase 8: Retention

#### Tasks

- [x] 8.1 `StoreMaintenanceWorkflow` and its activities. They create
  partitions two months ahead, drop partitions past `retention.events`,
  null `webhook_deliveries.payload` past `retention.delivery_payloads`,
  delete offboarded projections past `retention.offboarded`, and delete
  `runs` past `retention.events`.
- [x] 8.2 A daily Schedule (`store-maintenance`, overlap `SKIP`) when
  `store.enabled`.
- [x] 8.3 Tests (integration): each rule, with a clock override.

#### Success Criteria

- A month of fixtures past retention is gone after one run.
- Nothing inside the windows changes.

### Phase 9: The API contract and server

#### Tasks

- [x] 9.1 `api/openapi.yaml` (OpenAPI 3.1), written by hand:
  - every DESIGN-0004 endpoint, with snake_case fields;
  - open-enum descriptions;
  - the `Problem` schema with `request_id`;
  - cursor parameters, `security` on everything except `/status` and
    `/openapi.yaml`;
  - event kinds as a documented open enum, with per-kind `data` schemas
    (OQ5).
- [x] 9.2 `api/vacuum.yaml` (camel-case rule off) and `just lint-api`.
- [x] 9.3 `api/oapi-codegen.yaml` (std-http, strict server, models,
  embedded spec) → `internal/api/gen`, committed and drift-gated.
- [x] 9.4 Copy into `internal/api`, with provenance headers (OQ6):
  - `problem.go`, `cursor.go`, `middleware.go` (request ID, access log,
    recover);
  - `authn.go` (go-oidc, ID tokens refused, background discovery,
    `boopd_api_auth_failures_total`);
  - the contract-test helper `apitest`.

  Adapt names to boop, and keep no import of repo-guardian.
- [x] 9.5 `internal/api/authz.go`: the authz file
  (`owners`/`operators`/`clients`), visibility resolved per request, and
  SQL filtering helpers. A missing owner returns 404; a non-operator
  action returns 403.
- [x] 9.6 `boopd api --config <file>`: the read-only pool, Temporal
  client, OIDC verifier, health and readiness (store version, issuer
  discovered), metrics, and graceful stop.
- [x] 9.7 Contract tests: every handler's response is validated against
  the embedded spec, with a cursor-binding test and the authn and authz
  matrix.

#### Success Criteria

- `just lint-api` and the drift gates pass.
- The authn and authz matrix tests pass.
- `boopd api` serves `/api/v1/openapi.yaml`.

### Phase 10: API endpoints, recheck, status and estate

#### Tasks

- [x] 10.1 Read endpoints over sqlc queries:
  - `/me`, `/installations`, `/repositories` (+ `/{id}`, `/events`,
    `/runs`, `/dependencies`);
  - `/pull-requests` (+ detail), `/dashboard-items`, `/packages`,
    `/dependencies`, `/updates`;
  - `/runs`, `/events`, `/deliveries`.

  Each runs in a read-only transaction with a 5 s statement timeout.
- [x] 10.2 `/repositories/{id}` adds the Temporal `state` Query (a 2 s
  deadline, falling back to stored state with `live: false`).
- [x] 10.3 `POST /repositories/{id}/recheck` (operators):
  - it signals `recheck{actor, request_id}` and answers 202;
  - 409 `workflow_missing` when there is no workflow;
  - 503 when Temporal is unreachable;
  - a per-replica dedupe of one per repository per minute.
- [x] 10.4 `/estate` and `/estate/series`, computed on read and cached
  60 s per (scope, range, metric). The p95 is logged per query for the
  rollup decision.
- [x] 10.5 `/status`: computed every 30 s into a cache (components,
  fleet numbers, version), with `TestStatus_PrivacyGuard` (no free-text
  fields outside an allowlist).
- [x] 10.6 Tests (integration):
  - each endpoint over seeded data;
  - visibility filtering;
  - recheck's responses against the dev server;
  - estate numbers against a hand-computed fixture.

#### Success Criteria

- Every endpoint is contract-valid over the seed.
- A recheck from the API is recorded as `action.recheck` with the actor
  (through Phase 5.7).

### Phase 11: The UI scaffold on the design system

`ui/` starts from the mockup (Findings, 2026-11-21): the SPA shell and
four pages over fixtures. repo-guardian's `ui/` is read as the example;
no file is copied from it. Each task below says what the mockup already
covers.

What can start, and what waits:

| Work | Waits on |
| ---- | -------- |
| 11.1, 11.2, 11.4, 11.5, 11.7, 11.9 (shell) | nothing |
| 11.3 the BFF, against the mock issuer | nothing; its public-path check needs `api/openapi.yaml` (9.1) |
| 11.6 generated types and the API client, 11.8 `check-api` | `api/openapi.yaml` (9.1) |
| Phase 12 pages on real data | the API (Phases 9 and 10), or 9.1 alone if the pages are first built against typed fixtures |
| Playwright flows (12.8, 14) | the stack in k3d (Phases 13 and 14) |

`api/openapi.yaml` is written by hand from DESIGN-0004 and needs no
store code, so 9.1 is the one task that unblocks the rest of the UI.

#### Tasks

- [x] 11.1 Scaffold `ui/` to INV-0005 finding 7. The mockup has
  `package.json`, `tsconfig.json` with the strict flags and
  `web/vite.config.ts` with Tailwind. Still to add: `server/`,
  `scripts/` and `e2e/`, `bunfig.toml`, ESLint and the dev proxy.
  - one `package.json` over `server/`, `web/`, `scripts/` and `e2e/`,
    with repo-guardian's versions;
  - `tsconfig.json` with its strict flags;
  - `bunfig.toml` with a test preload that registers happy-dom before
    anything imports Base UI;
  - the ESLint config, with the ban on HTML sinks;
  - `web/vite.config.ts` with `@tailwindcss/vite` and the dev proxy for
    `/api`, `/auth` and `/ui`.
- [x] 11.2 The design system. Done in the mockup: the exact pin (0.4.0),
  `.npmrc`, `just ui-install`, `globals.css` and `.dark`. Still to add:
  the Renovate rule.
  - `@donaldgifford/design-system` pinned exactly, as a devDependency;
  - `ui/.npmrc` mapping the `@donaldgifford` scope to GitHub Packages
    and referencing `NPM_TOKEN`, with the value never stored;
  - `just ui-install` passes the token;
  - a `renovate.json5` rule that gives the package its own pull request,
    never grouped or automerged, and lets Renovate read the registry;
  - `globals.css` imported once from the SPA's entry, and `.dark` set on
    `<html>`.
- [x] 11.3 The BFF in `ui/server`, written here with repo-guardian's
  libraries and file layout (INV-0005 OQ6):
  - config from the environment, validated at start;
  - OIDC code + PKCE as the confidential client `boopd-ui`;
  - the sealed `__Host-boop_session` cookie, with key rotation and token
    refresh;
  - the `/api/*` proxy: GET and HEAD, plus `POST
    /api/v1/repositories/*/recheck` with an Origin check; the public
    paths checked against `api/openapi.yaml`;
  - static files with the `'self'` CSP, health, and `/ui/config` (the
    external links);
  - a mock OIDC issuer for tests.

  repo-guardian's server tests (app, auth, config, health, proxy,
  session, static) are ported as the behaviour to meet.
- [x] 11.4 The shell, composed from the package's parts. The mockup has
  the sidebar with the package's `AppSwitcher` as its header, the `NAV`
  groups, the account menu, and the top bar with the trigger, search
  button and `TimeZoneToggle`. Still to add: breadcrumbs, the external
  links group, sign out as a POST form, the `/me` gate, the route error
  view and the command palette.
  - `ShellSidebar` with the boop identity ("boop", "renovate · fleet"),
    the `NAV` groups FLEET / ACTIVITY / SYSTEM, the external links group
    when configured, and the account menu with sign out as a POST form;
  - `TopBarLayout` with the sidebar trigger, breadcrumbs, search and
    `TimeZoneToggle`;
  - the `/me` gate, the route error view and the command palette on
    `CommandDialog`.
- [x] 11.5 The badge state table on the package's `Badge` variants
  (DESIGN-0004 § Visual design); the open-enum fallback to `muted`
  (`ui/web/src/components/badges.tsx`; its tests are 11.9).
- [x] 11.6 Generated types from `api/openapi.yaml` (`generate-ui-api`),
  the drift check, and the API client with the package's problem
  helpers.
- [x] 11.7 `ui/Dockerfile` (Bun build, deps, distroless) with
  `NPM_TOKEN` as a build secret, and the `docker-bake.hcl` targets
  `ui-dev`, `ui-ci` and `ui-release`, with the same tag as `boopd`.
- [x] 11.8 Gates in `ui/scripts`: `check-api` (type drift), `check-dist`
  (no data: URL or Google Fonts URL in the build) and `check-tokens` (no
  colour outside the package's tokens).
- [x] 11.9 Tests: `bun test` for the server and the shell, and the CSP
  check.

#### Success Criteria

- `just ui-lint ui-test ui-build` passes.
- The image builds, no layer holds the registry token, and the shell
  renders against the mock issuer.
- `ui/web/src` holds no local copy of a component the package provides
  and defines no colour token.
- `grep -r repo-guardian ui/` matches nothing.

### Phase 12: UI pages

The mockup draws 12.1, 12.2, 12.4 (without Recheck and the Config tab)
and Status from 12.5, over `ui/web/src/fixtures.ts`. A page is done when
it reads the API, polls, and has its tests; the fixtures go when the last
page stops using them.

#### Tasks

- [x] 12.1 Estate (`/`): KPI strip, the open-PR trend, PR age buckets,
  runs by outcome, top pending packages, ecosystems, and the
  installations table.
- [x] 12.2 Pull requests (`/pulls`): a DataTable with filter chips, sorted
  oldest first, and a drawer with updates (package, from → to, purl),
  the timeline and a GitHub link.
- [x] 12.3 Waiting (`/waiting`): items grouped by section, with waiting
  since, ticked by and a dashboard link.
- [x] 12.4 Repository (`/repositories/$id`): a header with the live
  phase and Recheck (operators); tabs for Timeline (with causes), Runs,
  Pull requests, Waiting, Dependencies and Config.
- [ ] 12.5 Repositories, Packages (+ `$purl`), Runs, Events (15 s
  polling), Deliveries, and Status (public, outside the shell, 30 s
  refetch).
- [ ] 12.6 Polling (DESIGN-0004 OQ10): 30 s for lists, and 5 s on a
  repository with a run in progress.
- [ ] 12.7 Component gaps (INV-0005 OQ7, finding 8). For each page, list
  what the package lacks. A generic component is built in
  `donaldgifford/design-system` against a local link from this `ui/`,
  released, and the pin bumped here; it is not added under
  `ui/web/src`. The candidates known on 2026-11-21 were released in
  design-system 0.4.0, with the app switcher (its RFC-0002) and the
  status page parts:
  - stacked bars over time (12.1, runs by outcome);
  - a segmented control (the range control);
  - popover option rows (the owner scope);
  - a filter-chip bar (12.2);
  - one-line loading and empty text.

  A missing prop or variant is an issue on the design system. boop's own
  components (the update row, dashboard-item sections) stay in `ui/`.
- [ ] 12.8 Tests:
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
    `links` for the optional external links, `ingress` and `httpRoute`);
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
  - a UI job (install with `NPM_TOKEN` from `GITHUB_TOKEN` and
    `packages: read`, lint, test, build, check-api, check-dist,
    check-tokens);
  - an API lint job (vacuum, oapi-codegen drift, sqlc vet and drift);
  - Docker for the integration tests;
  - the UI e2e job.
- [ ] 15.2 `release.yml`: build (with the registry token as a build
  secret), sign (cosign) and attest (SLSA) the `boopd-ui` image with the
  same tag, and add it to the release notes.
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
| `ui/**`, `docker-bake.hcl`, `renovate.json5` | 11, 12 | BFF, SPA, image, the design-system rule |
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
- **`@donaldgifford/design-system`** from GitHub Packages (0.4.0 or
  later, pinned exactly), and a token with `read:packages` on every
  install path.
- **Design-system changes** (in its repository), done in 0.4.0:
  RFC-0001's entry rule amended so a component an app is building
  against counts, and the release that carries Phase 12.7's components.
  Open there: issues 29 (`Meter` direction) and 30 (`Timeline` time).
- **Copy source for Go code:** repo-guardian v2 @ `a72faf9`, read-only
  (Phase 9.4). Nothing depends on it after the copy.
- **Example for the UI:** repo-guardian `ui/` at `cc97e3f` or later, read
  and never copied.
- **Homelab:** CNPG, Keycloak and the Tailscale ingress (Phase 16).

## Open Questions

None open. The answered questions are listed under
[Resolved Questions](#resolved-questions).

## Resolved Questions

Answered 2026-10-18: option a for every question. OQ6 was amended on
2026-11-21 by INV-0005.

| OQ | Question | Decision |
| - | - | - |
| 1 | PR split? | **a**: three PRs: the store and writers, the API, the UI |
| 2 | Postgres for integration tests? | **a**: a pinned `postgres:17` container through Docker (`internal/store/storetest`) |
| 3 | Partitions? | **a**: native partitioning with `ensure_partitions`/`drop_partitions` from `StoreMaintenanceWorkflow` |
| 4 | oapi-codegen and sqlc pins? | **a**: `go tool` dependencies in `go.mod` |
| 5 | Dependency list size? | **a**: at most 2,000 in `RunResult.Dependencies`; past that, `RunRenovate` writes them straight to the store, or truncates with a flag |
| 6 | Copied-code marking? | **a**: a provenance header per copied file. Amended 2026-11-21: this covers the Go code only (Phase 9.4); nothing in `ui/` is copied, so there is no `ui/COPIED.md` |
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
- [INV-0005](../investigation/0005-boop-ui-its-own-app-on-the-design-system-or-a-backend-of-repo.md)
  (boop UI on the design system; the 2026-11-21 amendment)
- [design-system](https://github.com/donaldgifford/design-system): the
  package, RFC-0001 and the
  [Storybook](https://donaldgifford.github.io/design-system/)
- repo-guardian v2 @ `a72faf9` (the copy source for Go code): DESIGN-0027,
  DESIGN-0036, IMPL-0031, `internal/api`, `internal/store/postgres`
- repo-guardian `ui/` @ `cc97e3f` (the example for the UI): DESIGN-0039,
  IMPL-0037
- [pgx](https://github.com/jackc/pgx), [goose](https://github.com/pressly/goose),
  [sqlc](https://sqlc.dev), [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen),
  [go-oidc](https://github.com/coreos/go-oidc)
- [PostgreSQL table partitioning](https://www.postgresql.org/docs/current/ddl-partitioning.html)
- [Package URL specification](https://github.com/package-url/purl-spec)
