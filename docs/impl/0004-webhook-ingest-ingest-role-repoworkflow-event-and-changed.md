---
id: IMPL-0004
title: "Webhook ingest: ingest role, RepoWorkflow event and changed signals, delivery audit"
status: Draft
author: Donald Gifford
created: 2026-10-18
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0004: Webhook ingest: ingest role, RepoWorkflow event and changed signals, delivery audit

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Findings (2026-10-18)](#findings-2026-10-18)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Decisions and the replay baseline](#phase-1-decisions-and-the-replay-baseline)
  - [Phase 2: GitHub's webhook side in the fake](#phase-2-githubs-webhook-side-in-the-fake)
  - [Phase 3: Configuration](#phase-3-configuration)
  - [Phase 4: CheckRepo returns extends; a mint 404 is gone](#phase-4-checkrepo-returns-extends-a-mint-404-is-gone)
  - [Phase 5: RepoWorkflow event and changed](#phase-5-repoworkflow-event-and-changed)
  - [Phase 6: Targeted discovery](#phase-6-targeted-discovery)
  - [Phase 7: Delivery audit](#phase-7-delivery-audit)
  - [Phase 8: The ingest package](#phase-8-the-ingest-package)
  - [Phase 9: The ingest role](#phase-9-the-ingest-role)
  - [Phase 10: Chart](#phase-10-chart)
  - [Phase 11: End to end](#phase-11-end-to-end)
  - [Phase 12: Documentation](#phase-12-documentation)
  - [Phase 13: PRs, releases and the homelab](#phase-13-prs-releases-and-the-homelab)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [Resolved Questions](#resolved-questions)
- [References](#references)
<!--toc:end-->

## Objective

Build DESIGN-0003 (all OQs resolved):

- **An ingest role.** `boopd ingest` verifies the `boop-bot` App's
  webhook deliveries and turns each one into a Temporal signal on the
  workflow that owns the repository or installation.
- **Every delivery is recorded,** including ignored ones and boop-bot's
  own.
- **Two new `RepoWorkflow` signals.** `event` schedules a run after a
  30 s settle, at most once per 5 min except for checkbox clicks.
  `changed` runs `CheckRepo` first, then refreshes or offboards.
- **`CheckRepo` changes.** It returns `extends`, and a 404 on minting an
  installation token now means the installation is gone.
- **Targeted discovery** for a single installation.
- **A delivery audit.** A Schedule reads GitHub's delivery list and
  records failed and missed deliveries.
- **Chart support:** an ingest Deployment exposed by an Ingress or a
  Gateway API `HTTPRoute`, both off by default.

**Implements:** DESIGN-0003. It amends DESIGN-0001's non-goal "webhook
ingest" and closes INV-0001 OQ8.

## Scope

### In Scope

- `internal/workflows`:
  - the `event` and `changed` signals;
  - new `RepoState` fields;
  - `RunPlan.Settle` and `MinInterval`;
  - `DiscoveryInput.Installations`;
  - `DeliveryAuditWorkflow`;
  - the first replay tests.
- `internal/activities`: `CheckRepo` (extends), `PlanRun` (settle and
  min_interval), `ListHookDeliveries`.
- `internal/platform/github`: `CheckRepo` probing through
  `ProbeConfigREST`, the mint 404, and `ListHookDeliveries` with the App
  JWT.
- `internal/ingest` (new): signature check, App selection, routing, the
  delivery record and its `Sink`, the HTTP handler.
- `internal/config`: the `ingest` block and each App's `webhook` block.
- `cmd/boopd`: `boopd ingest`.
- `internal/worker`: the delivery-audit Schedules.
- `internal/observability`: the ingest and audit metrics.
- `charts/boopd`: the ingest Deployment, ServiceAccount, Service, PDB,
  Ingress, `HTTPRoute`, NetworkPolicy, the webhook Secret, values and
  schema, tests, and the PrometheusRule alerts.
- `test/fakegithub`: signed deliveries, the App hook-deliveries API, and
  deleted installations.
- `test/e2e`: the ingest scenarios.
- Docs: `docs/config.md`, a new `docs/webhooks.md`, and amendment notes
  in DESIGN-0001 and INV-0001.

### Out of Scope

- **Issue #16** (discovery honouring `Retry-After`). It lands first in its
  own `fix/issue-16` PR (DESIGN-0003 OQ12); see
  [Dependencies](#dependencies).
- **The store.** Records go to the log sink. IMPL-0005 phase 6 swaps the
  sink to the store and makes the audit and the decisions write rows.
- **Automatic redelivery.** The audit only records (DESIGN-0003 OQ5).
- **Static Temporal API keys.**

## Findings (2026-10-18)

From reading the code for this plan:

- **No replay tests exist yet.** `RepoWorkflow` changes shape here, so the
  baseline histories have to be recorded from the current release
  *before* Phase 5 touches `repo.go` (OQ3).
- **`recheck` is received with `c.Receive(ctx, nil)`** (`repo.go:162`,
  `drainRecheck`), so it discards any payload. IMPL-0005 adds an actor
  payload. This plan leaves `recheck` alone.
- **`plan()` runs at the start of each execution and before every run**
  (`repo.go:92`, `repo.go:262`). `settle` and `min_interval` from
  `PlanRun` are therefore current from the next run on. Between runs, a
  pending event uses the values from the last plan (OQ8).
- **`CheckRepo`** (`internal/platform/github/check.go:41`) already
  treats an archived repository as gone and uses `HasRenovateConfig`.
  `ProbeConfigREST` (from IMPL-0003) returns extends for one repository
  at the same cost.
- **`appFor` already falls back to listing** (`activities_impl.go:125`).
  With one App it returns that App. Otherwise it re-lists every App's
  installations and errors with "installation N is in no configured
  app's listing". A deleted installation therefore never reaches the
  token mint when several Apps are configured. Phase 4 maps "absent from
  a fresh listing" to gone, as well as the mint 404.
- **The App JWT path exists.** `ListInstallations` already calls
  App-level endpoints. go-github v62 (already a direct dependency) has
  `AppsService.ListHookDeliveries` (cursor paging), so the audit uses the
  same transport and library.
- **go-github covers webhooks.** v62's `ValidatePayloadFromBody` (HMAC
  with `hmac.Equal`, JSON and form bodies), `WebHookType`, `DeliveryID`
  and `ParseWebHook` give typed events with every field routing needs:
  `EditChange.Body.From`, push commits' `Added`/`Modified`/`Removed`, and
  `PerformedViaGithubApp` (OQ4). It also accepts `sha1=` signatures, so
  ingest enforces `sha256=` itself.
- **Exposure in the homelab.** A Tailscale ingress is reachable only
  inside the tailnet. GitHub needs a public HTTPS endpoint for the
  webhook path, which means Tailscale Funnel for that host or another
  public route (OQ11).
- **The config has a completeness test.** `reference_test.go` keeps
  `docs/config.md` complete, so every new setting needs its row.

## Implementation Phases

Each phase ends with `just lint`, `just test` and the phase's own checks
green, and one commit per numbered task. The PR split is in OQ1.

### Phase 1: Decisions and the replay baseline

#### Tasks

- [x] 1.1 DESIGN-0003 status → Approved; DESIGN-0001's non-goals line
  "Webhook ingest (INV-0001 OQ8)" gains "→ DESIGN-0003"; INV-0001 OQ8
  gets a "resolved by DESIGN-0003" note.
- [x] 1.2 `internal/workflows/testdata/replay/`: record histories from
  the **current** code with the dev-server test helper
  (`internal/temporal/temporaltest`) under the integration tag:
  - a `RepoWorkflow` that discovered, ran once, received a `recheck`,
    ran again and continued as new;
  - a `RepoWorkflow` that hit the absence check (present, then gone);
  - a `DiscoveryWorkflow` pass over two installations.

  A `go:generate`-style recorder test (`TestRecordReplayHistories`,
  skipped unless `BOOPD_RECORD_HISTORIES=1`) writes the JSON histories.
- [x] 1.3 `TestReplay_Histories` replays every file through
  `worker.NewWorkflowReplayer` with the registered workflows. It runs in
  `just test` (no dev server needed).

#### Success Criteria

- The replay test passes on today's code.
- The histories are committed and named for what they exercise.
- markdownlint passes on the amended docs.

### Phase 2: GitHub's webhook side in the fake

#### Tasks

- [x] 2.1 `fakegithub.Delivery` and `(*Server).Deliver(t, url, event,
  payload any)`. It signs with the configured secret (`X-Hub-Signature-256`)
  and sets `X-GitHub-Event`, `X-GitHub-Delivery` (a UUID, or a given one
  for redelivery tests) and `X-GitHub-Hook-Installation-Target-ID`/`-Type`.
  It records the response status for the hook-deliveries API.
- [x] 2.2 Payload builders for the routed events, trimmed to the fields
  ingest reads:
  - `issues.edited`, `pull_request` (`edited`, `closed`);
  - `push` (with commits' `added`, `modified`, `removed`, and a
    commit-cap variant);
  - `repository` (each routed action);
  - `installation` and `installation_repositories`;
  - `ping`.
- [x] 2.3 `GET /app/hook/deliveries` (App JWT, `per_page`, `cursor` link
  paging, newest first) over the recorded deliveries. It returns
  `guid`, `delivered_at`, `redelivery`, `status_code`, `event`, `action`,
  `installation_id` and `repository_id`.
  `(*Server).RecordUndelivered(event, action)` adds a delivery GitHub
  "sent" that never reached ingest, for the missed case.
- [x] 2.4 `(*Server).DeleteInstallation(id)`: token minting for that
  installation answers 404 from then on.
- [x] 2.5 Unit tests for each knob in `fakegithub_test.go`.

#### Success Criteria

- `go test ./test/fakegithub/...` passes.
- A delivery signed by the fake verifies with the same secret and fails
  with another.
- The hook-deliveries list pages as GitHub's does.

### Phase 3: Configuration

#### Tasks

- [x] 3.1 `internal/config/schema.go`: an optional `ingest` block
  (`path`, `settle`, `min_interval`, `audit_every`) and an optional
  per-App `webhook` block (`bot`, `secret_ref`).
- [x] 3.2 `config.go`, `validate.go`, defaults and validation:
  - `path` defaults to `/webhooks/github` and must start with `/`;
  - `settle` defaults to `30s` and `min_interval` to `5m`, both at least
    `0`;
  - `audit_every` defaults to `15m`, at least `1m`;
  - `bot` defaults to `<app label>[bot]`;
  - the secret must be non-empty when read.

  Each has diagnostics with ranges, as for the existing blocks.
- [x] 3.3 `secrets.go`: read each webhook secret the same way as the
  App key.
- [x] 3.4 `PlanRun` returns `Settle` and `MinInterval` from the config;
  `RunPlan` gains the fields.
- [x] 3.5 `examples/boopd.hcl` gains a commented `ingest` block and a
  `webhook` block on `boop-bot`; `boopd config validate` keeps it
  loadable.
- [x] 3.6 `docs/config.md` rows for every new setting, with defaults and
  chart keys.

#### Success Criteria

- `go test ./internal/config/...` passes, including the golden and
  reference-completeness tests.
- `boopd config validate examples/boopd.hcl` passes.
- An invalid `path` or a negative `settle` gets a ranged diagnostic.

### Phase 4: CheckRepo returns extends; a mint 404 is gone

#### Tasks

- [x] 4.1 `internal/platform/github/check.go`: `CheckRepo` probes with
  `ProbeConfigREST` for the one repository and returns its `Extends`
  (`ParseErr` is a warn log in the activity). The signature gains the
  config paths it needs (the client already carries them).
- [x] 4.2 The installation-token mint answers 404 →
  `platform.ErrInstallationGone`. `CheckRepo` (the activity) maps it to
  `RepoGone`. Every other mint error stays an activity error.
- [x] 4.3 `CheckRepoResult.Extends`; the absence check in `repo.go`
  applies `Extends` when `present` (the refresh also helps discovery).
- [x] 4.4 `appFor`: "absent from every App's fresh listing" becomes
  `platform.ErrInstallationGone`, which `CheckRepo` maps to `RepoGone`.
  A listing failure stays an error. There are tests for one App (the
  mint 404 path) and two Apps (the listing path).
- [x] 4.5 Tests:
  - `check_test.go`: extends returned, no config, archived, deleted,
    mint 404 against fakegithub;
  - activity tests: `gone` from a mint 404, and a transient mint error
    still errors.

#### Success Criteria

- `go test ./internal/platform/... ./internal/activities/...` passes.
- The replay test still passes. `CheckRepoResult` grew a field, and old
  histories decode with it empty.

### Phase 5: RepoWorkflow event and changed

#### Tasks

- [x] 5.1 `names.go`: `EventSignal = "event"`, `ChangedSignal =
  "changed"`; `run_types.go`: `Event{Reason string; Priority Priority}`,
  `Changed{Reason string; Slug, DefaultBranch string; InstallationID
  int64}`.
- [x] 5.2 `RepoState` gains `EventDue time.Time`, `EventPriority
  Priority`, `EventReason string`.
- [x] 5.3 `wait()` changes:
  - a pending `changed` is drained before the due check and runs
    `checkRepo` (ending on `gone` or `no-config`; on `present` it applies
    the refresh and re-plans with `plan()`);
  - `event` sets `EventDue` by the DESIGN-0003 rule (`max(now + settle,
    lastStart + min_interval)`, high priority skips the second term);
    later events keep the earlier `EventDue` and the highest priority;
  - the due timer is `min(NextDue, EventDue)`;
  - `changed{present}` becomes an `event{changed, normal}`.
- [x] 5.4 `runOnce` clears `EventDue` when a run starts. Events received
  during a run stay buffered and are applied after it, so they lead to
  one further run.
- [x] 5.5 `continueAsNew` carries `EventDue`, `EventPriority` and
  `EventReason`, and drains buffered `event` and `changed` signals into
  the state as it does `discovered` and `recheck`.
- [x] 5.6 A workflow started by `SignalWithStart(changed)`: the start
  path skips `firstDueJitter` when a `changed` is pending, so
  `CheckRepo` runs before any run.
- [x] 5.7 Decision log lines: `onboarded`, `offboarded` (with reason),
  `refreshed`, `run-scheduled` (with reason, priority and time), each
  with the cause (the delivery ID from the signal). Add
  `boopd_repo_event_runs_total{reason}` (workflow metric).
- [x] 5.8 Tests (`repo_test.go`, testsuite):
  - an event settles and merges with later ones;
  - `min_interval` holds for normal events but not for high ones;
  - an event during a run gives exactly one more run;
  - `changed` → `gone` ends the workflow, and `present` re-plans then
    runs;
  - `SignalWithStart(changed)` checks before its first run, even when
    `NextDue` is past;
  - ContinueAsNew carries the pending event;
  - `recheck` is unchanged.
- [x] 5.9 The replay test passes over the Phase 1 histories. If it shows
  a new command on an old history, gate the change with
  `workflow.GetVersion("webhook-signals", …)` (as `capacityLeaseChange`
  does) and re-run.

#### Success Criteria

- `go test ./internal/workflows/...` passes, including replay.
- The settle and min_interval timings are asserted with the test clock,
  not sleeps.

### Phase 6: Targeted discovery

#### Tasks

- [x] 6.1 `DiscoveryInput.Installations []int64`. When it is set,
  `DiscoveryWorkflow` filters `ListInstallations`' result to those IDs and
  ignores the App's allowlist only if an ID is in it. An ID outside the
  allowlist is skipped with a warn.
- [x] 6.2 `workflows.TargetedDiscoveryID(app, installationID)` →
  `discovery/<app>/installation/<id>`.
- [x] 6.3 Tests: a targeted pass probes one installation only; an
  allowlisted-out ID is skipped; the Schedule's untargeted pass is
  unchanged (replay).

#### Success Criteria

- `go test ./internal/workflows/... ./internal/activities/...` passes,
  replay included.

### Phase 7: Delivery audit

#### Tasks

- [x] 7.1 `internal/platform/github`: `AppClient.ListHookDeliveries(ctx,
  cursor string, perPage int)`, a thin wrapper over go-github's
  `Apps.ListHookDeliveries` with the App JWT. It returns the deliveries
  and the next cursor, classifies errors through `classifyErr`, and
  passes primary-limit errors through as `RateLimitedError`.
- [x] 7.2 `ListHookDeliveries` activity: pages newest first until it
  reaches the previous cursor (the newest GUID seen) or GitHub's 3-day
  window ends. It classifies each delivery as `failed` (non-2xx),
  `redelivered` (a later 2xx for the same GUID) or `ok`.
- [x] 7.3 Missed deliveries. Until the store exists, ingest's record of
  a GUID is not queryable, so `missed` is detected only through GitHub's
  own status: a connection failure or timeout reports a non-2xx or
  empty status. The activity counts those as `failed`, and true `missed`
  (2xx at GitHub, no ingest record) starts with IMPL-0005 phase 6
  (OQ6).
- [x] 7.4 `DeliveryAuditWorkflow(app)`: one activity call, records the
  counts, and continues as new with the cursor. The cursor bounds the
  next pass.
- [x] 7.5 `internal/worker/schedules.go`: one Schedule per App with a
  `webhook` block, every `ingest.audit_every`, overlap `SKIP`, ID
  `delivery-audit/<app>`.
- [x] 7.6 Metrics `boopd_ingest_deliveries_failed_total{app,event}` and
  `boopd_ingest_deliveries_missed_total{app,event}` (the latter
  registered now, incremented from IMPL-0005).
- [x] 7.7 Tests: activity against fakegithub (failed, redelivered,
  cursor stop, lost cursor re-reads idempotently), workflow testsuite,
  and `schedules_test.go` for the per-App Schedule.

#### Success Criteria

- `go test ./internal/...` passes.
- `EnsureSchedules` creates the audit Schedules only for Apps with a
  webhook.

### Phase 8: The ingest package

#### Tasks

- [x] 8.1 `internal/ingest/verify.go`: select the App by
  `X-GitHub-Hook-Installation-Target-ID`, require `X-Hub-Signature-256`
  with a `sha256=` prefix (so a `sha1`-only delivery is rejected), and
  call go-github's `ValidatePayloadFromBody` on the size-limited body
  with that header only. Missing, malformed and wrong signatures are
  rejected.
- [x] 8.2 `internal/ingest/payload.go`: `github.ParseWebHook(event,
  body)` into go-github's typed events (OQ4). `Route` takes the typed
  event, and a small adapter reads the shared fields (sender,
  installation, repository) across event types. An event type go-github
  does not know is recorded and ignored.
- [x] 8.3 `internal/ingest/route.go`: `Route(event string, p *Payload,
  app *config.App, paths []string) []Action`, a pure function
  implementing DESIGN-0003's table top to bottom:
  - the own-bot filter;
  - the issue- and PR-author checks;
  - the push rules (default branch, config paths, commit cap);
  - repository actions;
  - installation and installation-repositories events.

  `Action` is `{Kind: signal|signalWithStart|startDiscovery; WorkflowID;
  Signal; Arg; Start *workflows.RepoState}`.
- [x] 8.4 `internal/ingest/record.go`: the `Record` type (DESIGN-0003
  § Recording deliveries) and `Sink` interface. `LogSink` writes one info
  line per delivery (headers only for a rejected one).
- [x] 8.5 `internal/ingest/handler.go`: the handler.
  - It enforces `POST` on `ingest.path`, reads at most 25 MiB (413
    above), verifies, answers `ping` with 200, routes, and runs the
    actions with a 5 s deadline.
  - It answers 202 on success or nothing-to-do, 401, 400 or 413 on
    rejection, and 503 on a Temporal failure.
  - It treats `NotFound` on `Signal` as ignored, then records the
    delivery.
  - It takes a `Temporal` interface (`SignalWorkflow`,
    `SignalWithStartWorkflow`, `ExecuteWorkflow` with
    `WorkflowIDConflictPolicy: USE_EXISTING`).
- [x] 8.6 Metrics: `boopd_ingest_deliveries_total{app,event,outcome}`
  and `boopd_ingest_temporal_seconds{action}` in
  `internal/observability`.
- [x] 8.7 Tests:
  - `route_test.go`: a table over testdata payloads, one case per
    DESIGN-0003 row plus own-bot, unknown App, commit cap and non-default
    branch;
  - `verify_test.go`: valid, wrong secret, missing header, `sha1=` only
    (rejected even though go-github would accept it), body changed after
    signing, form-encoded body;
  - `handler_test.go`: status codes, deadline, `NotFound` ignored, the
    sink called on every path, with a fake `Temporal`.

#### Success Criteria

- `go test ./internal/ingest/...` passes with branch coverage of every
  routing row.
- `golangci-lint` is clean.

### Phase 9: The ingest role

#### Tasks

- [x] 9.1 `cmd/boopd`: `boopd ingest --config <file>`, with the usage
  text updated. It loads the config and reads only the webhook secrets
  (never the App key). It dials Temporal with the shared `TEMPORAL_*`
  settings and serves the handler on `LISTEN_ADDR`, with `/healthz`,
  `/readyz` (Temporal dial succeeded) and metrics on `METRICS_ADDR`.
- [x] 9.2 Graceful stop: on SIGTERM, `/readyz` fails, then the server
  drains in-flight requests (`Shutdown` with a timeout below the pod's
  grace period).
- [x] 9.3 Fail at start if no App has a `webhook` block.
- [x] 9.4 `main_test.go`: usage, a missing config, no webhook App, and a
  start and stop against the dev server (integration tag).

#### Success Criteria

- `boopd ingest` starts against the dev server, answers `ping`, and
  stops cleanly on SIGTERM.

### Phase 10: Chart

#### Tasks

- [x] 10.1 `values.yaml` and `values.schema.json`: `ingest.*` as in
  DESIGN-0003 § Chart (`enabled: false`, `replicaCount: 2`, `pdb`,
  `service`, `ingress`, `httpRoute`, `networkPolicy.from`, `resources`),
  and `boopd.ingest` / `boopd.apps[].webhook` rendered into `boopd.hcl`.
- [x] 10.2 `templates/ingest.yaml`:
  - a Deployment (`boopd ingest`) with the rendered config;
  - the webhook Secret mounts and Temporal credentials, but no App key;
  - its own ServiceAccount with no token mount;
  - a Service and a PDB;
  - the same security context as the worker.
- [x] 10.3 `templates/ingest-ingress.yaml` and
  `templates/ingest-httproute.yaml`, each routing only `ingest.path`.
  The `HTTPRoute` renders when `ingest.httpRoute.enabled` is set.
- [x] 10.4 `templates/networkpolicy.yaml`: ingest ingress from
  `ingest.networkPolicy.from` on the HTTP port and from Prometheus on
  metrics; egress to DNS and Temporal only.
- [x] 10.5 `templates/prometheusrule.yaml`: `BoopdIngestFailing` and
  `BoopdDeliveriesLost`.
- [x] 10.6 Guards: render fails if `ingest.enabled` is set without an
  App `webhook` block, or if both routes are enabled with the same
  host.
- [x] 10.7 helm-unittest suites `tests/ingest_test.yaml`:
  - no App key and no ServiceAccount token;
  - both routes limited to the path;
  - the NetworkPolicy has no GitHub egress;
  - the guards;
  - the PDB;
  - the schema rejects unknown keys.
- [x] 10.8 `just helm-docs`; `boopd config validate` on the rendered
  config in `just helm-test`.

#### Success Criteria

- `just helm-test` passes (lint, ct, unittest, rendered-config validate).
- Rendering with `ingest.enabled=false` is byte-identical to before for
  every existing resource.

### Phase 11: End to end

#### Tasks

- [x] 11.1 Harness: the chart e2e installs with `ingest.enabled=true`
  and a webhook secret. The test reaches the ingest Service through a
  port-forward and sends deliveries with `fakegithub.Deliver` (OQ7).
- [x] 11.2 Scenarios (`test/e2e/ingest_test.go`):
  - `TestIngest_DashboardTickRuns`: a user `issues.edited` on the
    bot's issue runs within settle plus margin;
  - `TestIngest_OwnBotIgnored`: the same edit with the bot as sender
    gives no run;
  - `TestIngest_ConfigPushOnboards`: a config-adding push for an
    unknown repository starts its RepoWorkflow and runs;
  - `TestIngest_ConfigRemovedOffboards`: a config-removing push ends
    the workflow;
  - `TestIngest_RepositoriesAddedDiscovers`: targeted discovery for one
    installation;
  - `TestIngest_InstallationDeleted`: the mint answers 404 and the
    repositories offboard;
  - `TestIngest_BadSignature`: 401 and no Temporal call;
  - `TestIngest_TemporalDown`: scale the dev server's forwarder away and
    get 503;
  - `TestDeliveryAudit_RecordsFailures`: the audit Schedule counts the
    failed delivery from the previous case.
- [x] 11.3 CI's "E2E Tests" job runs the new scenarios (no workflow file
  change if they sit under `./test/e2e/...`).

Notes from the build:

- The scenarios are ordered subtests of one `TestIngest` (for example
  `TestIngest/DashboardTickRuns`) on one install, since later cases build
  on earlier ones and the audit counts TemporalDown's 503. The
  forwarder is the stub-github image in a TCP-forward mode.
- `InstallationDeleted` found a gap: the cached installation client
  kept its token, so `CheckRepo` never minted and saw the repository
  present. GitHub revokes a deleted installation's tokens, so
  `CheckRepo` now forgets the client on 401 and checks again with a
  fresh mint (also on PR A).

#### Success Criteria

- `just e2e` passes locally and in CI.
- Each scenario asserts the Temporal outcome, via workflow state or
  history, not only the HTTP status.

### Phase 12: Documentation

#### Tasks

- [x] 12.1 `docs/webhooks.md` (OQ10), a usage guide:
  - App settings and the events to subscribe;
  - creating the secret;
  - exposure with an Ingress or `HTTPRoute` (and Tailscale Funnel);
  - verifying with `ping` and Recent deliveries;
  - what is routed and what is recorded;
  - reading the audit metrics;
  - rollback.
- [x] 12.2 `docs/config.md`: final rows checked against the code.
- [x] 12.3 Chart README regenerated; CLAUDE.md's layout gains
  `internal/ingest/` and the role list `boopd ingest`.
- [ ] 12.4 DESIGN-0003 status → Implemented once Phase 13.2 ships.
  `deferred - human required`: waits on merging #24.

#### Success Criteria

- markdownlint and the reference-completeness test pass.
- A reader can enable ingest from `docs/webhooks.md` alone.

### Phase 13: PRs, releases and the homelab

#### Tasks

- [x] 13.1 #23: PR A (worker side, phases 1–7), label `minor`. Merging
  releases it. With no ingest the signals are inert; the audit reads
  only.
- [x] 13.2 PR B (ingest, chart, e2e, docs; phases 8–12), label `minor`.
  #24 (PR A is #23).
- [ ] 13.3 **Deferred, human required.** Homelab setup:
  - create the Keycloak client `boopd-ingest` (client credentials, the
    Temporal audience, the namespace writer role);
  - create the webhook Secret;
  - enable `ingest` with the Tailscale ingress and Funnel for the
    webhook host (OQ11);
  - set the App webhook URL, secret and subscriptions (Issues, Pull
    request, Push, Repository);
  - see `ping` succeed in Recent deliveries.
- [ ] 13.4 **Deferred, human required.** Tick a box on the
  proxmox-go-sdk dashboard and time it to the run. Push a config to a
  scratch repository and time the onboarding. Record both here.
- [ ] 13.5 IMPL status → Completed, with the evidence.
  `deferred - human required`: waits on 13.3 and 13.4.

#### Success Criteria

- Both releases are out, and ingest is enabled on fartlab.
- A dashboard tick runs Renovate in under 2 minutes.
- No `BoopdDeliveriesLost` alert in the first week.

## File Changes

| File | Phase | Change |
| - | - | - |
| `docs/design/0001-…md`, `docs/investigation/0001-…md`, DESIGN-0003 | 1, 12 | amendment notes, status |
| `internal/workflows/testdata/replay/*.json`, `replay_test.go` | 1 | baseline histories, replay test |
| `test/fakegithub/fakegithub.go` (+ test) | 2 | `Deliver`, payload builders, hook deliveries, deleted installations |
| `internal/config/{schema,config,validate,secrets}.go` (+ tests, golden) | 3 | `ingest` and `webhook` blocks |
| `examples/boopd.hcl`, `docs/config.md` | 3, 12 | examples and rows |
| `internal/platform/github/check.go`, `app.go` (+ tests) | 4, 7 | extends, mint 404, `ListHookDeliveries` |
| `internal/activities/{repo,plan,deliveries}.go` (+ tests) | 3, 4, 7 | `CheckRepo`, `PlanRun`, `ListHookDeliveries` |
| `internal/workflows/{names,run_types,repo,discovery,deliveries}.go` (+ tests) | 5, 6, 7 | signals, state, targeted discovery, audit workflow |
| `internal/worker/schedules.go` (+ test) | 7 | audit Schedules |
| `internal/observability/metrics.go` (+ test) | 5, 7, 8 | ingest, audit and event-run metrics |
| `internal/ingest/*` | 8 | new package |
| `cmd/boopd/{main,ingest}.go` (+ test) | 9 | `boopd ingest` |
| `charts/boopd/{values.yaml,values.schema.json,templates/*,tests/ingest_test.yaml,README.md}` | 10 | ingest resources |
| `test/e2e/ingest_test.go`, `chart_test.go` | 11 | scenarios |
| `docs/webhooks.md`, `CLAUDE.md` | 12 | usage, layout |

## Testing Plan

E2E first, per IMPL-0001 OQ3 and the repository's rule:

- **k3d e2e carries the main paths:** delivery to signal to run,
  onboarding, offboarding, targeted discovery, rejection and Temporal
  failure. They run against the real chart, real Temporal (the dev
  server), stub Renovate and `test/fakegithub`.
- **Replay tests** guard `RepoWorkflow` and `DiscoveryWorkflow` against
  non-determinism, starting from histories recorded before the change.
- **Testsuite workflow tests** cover the timing rules (settle,
  min_interval, buffering, ContinueAsNew) on the test clock.
- **Unit tests** cover what e2e reaches only at a cost: every routing
  row, every signature failure, the handler's status codes and the
  config diagnostics.
- **Chart unittests** cover what the ingest pod must never have.

## Dependencies

- **#16 merges first** (`fix/issue-16`, `patch`). The targeted discovery
  here makes discovery more frequent, and #16 makes it honour
  `Retry-After`.
- **No new Go modules.** go-github v62 (already direct) provides the
  webhook validation, typed events and the App hook-deliveries API
  (OQ4).
- Phase 13.3 needs Keycloak, a public route to the cluster (Funnel), and
  App-admin access on `boop-bot`.
- IMPL-0005 phase 6 depends on Phase 8's `Sink` and Phase 7's audit.

## Open Questions

None open. The answered questions are listed under
[Resolved Questions](#resolved-questions).

## Resolved Questions

Answered 2026-10-18: option a for every question except OQ4, where
go-github's webhook support (option b) was chosen on review.

| OQ | Question | Decision |
| - | - | - |
| 1 | How is the work split into PRs? | **a**: PR A, the worker side (phases 1–7), then PR B, ingest, chart, e2e and docs (phases 8–12) |
| 2 | Release labels? | **a**: `minor` for both |
| 3 | Replay safety? | **a**: record histories from the current code first; replay in `just test`; `GetVersion` only if replay shows a new command |
| 4 | How are payloads decoded? | **b**, reconsidered: go-github v62 (already a direct dependency): `ValidatePayloadFromBody` with only `X-Hub-Signature-256` and a `sha256=` prefix check, `ParseWebHook` typed events, and `Apps.ListHookDeliveries` for the audit |
| 5 | Config paths for push matching? | **a**: `renovate.config_paths`, the list discovery probes |
| 6 | When does "missed" count? | **a**: from IMPL-0005 phase 6; registered at 0 until then |
| 7 | How do e2e tests send deliveries? | **a**: from the test process through a port-forward, with `fakegithub.Deliver` |
| 8 | Settle and min_interval in running workflows? | **a**: through `PlanRun`, which runs before every run |
| 9 | Targeted discovery and the allowlist? | **a**: respected; IDs outside it are skipped with a warn |
| 10 | Usage guide? | **a**: a new `docs/webhooks.md` |
| 11 | Homelab exposure? | **a**: the Tailscale ingress with Funnel for a dedicated webhook host, routing only the webhook path |
| 12 | Where does the replay recorder live? | **a**: a test in `internal/workflows` behind `BOOPD_RECORD_HISTORIES=1` and the integration tag |

## References

- DESIGN-0003 (webhook ingest, resolved OQs)
- DESIGN-0004 / IMPL-0005 (the store that replaces the log sink)
- DESIGN-0001 § RepoWorkflow; INV-0001 OQ8; ADR-0002; ADR-0005; ADR-0008
- IMPL-0003 (`ProbeConfigREST`, probe cost)
- Issue #16
- [Validating webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries)
- [REST API endpoints for GitHub App webhooks](https://docs.github.com/en/rest/apps/webhooks)
- [Temporal Go SDK: replay testing](https://docs.temporal.io/develop/go/testing-suite#replay)
