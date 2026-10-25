---
id: IMPL-0006
title: "Sandboxed execution of repository code in Renovate runs"
status: Draft
author: Donald Gifford
created: 2026-10-25
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0006: Sandboxed execution of repository code in Renovate runs

<!--toc:start-->
<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Findings (2026-10-25)](#findings-2026-10-25)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Verify the platform assumptions](#phase-1-verify-the-platform-assumptions)
  - [Phase 2: The floor list and the Renovate drift test](#phase-2-the-floor-list-and-the-renovate-drift-test)
  - [Phase 3: Configuration](#phase-3-configuration)
  - [Phase 4: The profile resolver](#phase-4-the-profile-resolver)
  - [Phase 5: The Job builder](#phase-5-the-job-builder)
  - [Phase 6: The narrowed token](#phase-6-the-narrowed-token)
  - [Phase 7: Floor detection in PlanRun](#phase-7-floor-detection-in-planrun)
  - [Phase 8: RunRenovate, RepoWorkflow and failing closed](#phase-8-runrenovate-repoworkflow-and-failing-closed)
  - [Phase 9: Observability](#phase-9-observability)
  - [Phase 10: Chart](#phase-10-chart)
  - [Phase 11: End to end](#phase-11-end-to-end)
  - [Phase 12: Documentation](#phase-12-documentation)
  - [Phase 13: Releases and the homelab](#phase-13-releases-and-the-homelab)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:objective:start-->
## Objective

Build DESIGN-0005's sandboxed tier, with every DESIGN-0005 open question
resolved as recorded there (2026-10-25):

- **A floor** of Renovate managers that execute repository code with no
  Renovate gate. A repository with one runs only in the sandbox.
  Ordinary runs force those managers off, so they never execute outside
  it.
- **A `sandbox` block and sandboxed profiles** in `boopd.hcl`. A
  sandboxed profile's `unsafe_executions` is the only path to Renovate's
  `allowedUnsafeExecutions`.
- **Sandboxed Jobs:** Kata's RuntimeClass, any Kata-capable worker by
  default (dedicated nodes optional), a no-RBAC ServiceAccount, no
  shared Redis, an egress policy for `cilium`, `kubernetes` or
  `vpc-cni`, and a token without `workflows`.
- **Fail closed:** a run that needs the sandbox and cannot have it does
  not happen, and says so (`needs-sandbox`).
- **A drift test** that ties the floor, the gated list and the floor
  managers' file patterns to the pinned Renovate image.

**Implements:** DESIGN-0005. Re-scopes IMPL-0001 8.8, which runs against
this tier once Phase 13 is done.
<!--docz:objective:end-->

<!--docz:scope:start-->
## Scope

<!--docz:in-scope:start-->
### In Scope

- Go:
  - `internal/profiles`: the floor, raising to a sandboxed profile;
  - `internal/renovate/unsafe` (new): the floor list, the gated list and
    the floor managers' default `managerFilePatterns`, generated from
    the pinned image;
  - `internal/config`: the `sandbox` block, `sandboxed`,
    `unsafe_executions`, `egress_allow`, `floor_mode`, `floor_add`,
    `sandbox.token_permissions`, validation, `BuildInput`;
  - `internal/jobspec`: the sandboxed pod spec, the forced floor
    disable, `allowedUnsafeExecutions`, `force` added to the forbidden
    options;
  - `internal/platform` and `internal/platform/github`: `Mint` with
    permissions; a tree listing;
  - `internal/activities`: PlanRun's floor detection; RunRenovate's
    sandbox check and the `NeedsSandbox` error;
  - `internal/workflows`: `RunPlan.Sandboxed`, the `needs-sandbox`
    phase, the `Sandboxed` search attribute, `RunResult` fields, a
    versioned branch;
  - `internal/worker` and `internal/observability`: the sandbox
    readiness gauge and the new metrics.
- `test/fakegithub`: the tree endpoint, recorded mint permissions.
- `test/e2e`: sandbox scenarios in k3d.
- `charts/boopd`: values, schema, the ServiceAccount, the egress
  policies, RBAC for the RuntimeClass check, quota, alerts, NOTES.
- Docs: `docs/sandbox.md` (new), `docs/config.md`, `examples/boopd.hcl`,
  CLAUDE.md, DESIGN-0005 status.
<!--docz:in-scope:end-->

<!--docz:out-of-scope:start-->
### Out of Scope

- **A Kata VMM other than the RuntimeClass's default** (INV-0003).
- **Nix flake updates** (INV-0004).
- **Splitting the token from the execution** (DESIGN-0005 OQ4 b).
- **A sandbox cache** (DESIGN-0005 OQ7 b), until scale asks for it.
- **An egress proxy** for name-based filtering without Cilium.
- **User namespaces for ordinary Jobs** (OQ8 here).
- **The DESIGN-0004 store columns**: the fields land in `RunResult`;
  the store picks them up when IMPL-0005 runs.
<!--docz:out-of-scope:end-->
<!--docz:scope:end-->

## Findings (2026-10-25)

From reading the code and running the pinned image for this plan:

- **The chart pins `renovate:44-full` 44.149.2** (`charts/boopd/values.yaml`)
  and the builder sets `binarySource=global`. The tools are in the image
  (`pip-compile`, `uv`, `poetry`, `pdm`, `pipenv`, `bundler`, `pod`,
  `mix`, `dotnet`, `conan`, `gradle`, `java`, `bazel`, `mise`, `go`;
  `nix` as a shim; no `mvn`, no `pixi`). Its `allowedUnsafeExecutions`
  list matches 44.179.2's, and `maven-wrapper` is ungated in both.
- **Forcing a floor manager off works only with its default patterns.**
  `RENOVATE_FORCE='{"pip-compile":{"enabled":false}}'` failed the
  repository ("managerFilePatterns is not iterable"). Adding the
  manager's default `managerFilePatterns` to the forced object worked:
  `pip-compile` left the report of `boop-fixture-python-sdist` and the
  other managers stayed (`dryRun: extract`).
  The builder therefore needs each floor manager's defaults, which
  Phase 2 generates.
- **Kata rejects `hostUsers: false`.** Kubernetes documents user
  namespaces for runtimes that isolate with Linux namespaces, and Kata's
  handler reports "does not support user namespaces". DESIGN-0005 no
  longer sets it on sandboxed Jobs; Phase 1 confirms it in the homelab.
- **The pod posture is already restricted** (`internal/jobspec/job.go`
  `podTemplate`): non-root, read-only root, all capabilities dropped,
  `RuntimeDefault` seccomp, no ServiceAccount token, no service links,
  `emptyDir` work and `/tmp`, `activeDeadlineSeconds`. Profiles already
  carry `RuntimeClassName`, `NodeSelector` and `Tolerations`
  (`PodOverlay`).
- **`Minter.Mint` takes repository IDs only** (`internal/platform/platform.go`);
  the GitHub client sends `repository_ids` and no `permissions`, so the
  token has every permission the App has.
- **`RepoState.Managers` only grows** (`mergeManagers`), which suits the
  floor: a repository seen with a floor manager stays sandboxed.
- **RepoWorkflow is already versioned** (`GetVersion` for
  `capacity-lease-1`) and replay-tested
  (`internal/workflows/testdata/replay/`); the `needs-sandbox` branch
  follows the same pattern.
- **The worker's Role is namespaced** (`charts/boopd/templates/rbac.yaml`);
  checking that a RuntimeClass exists is a cluster-scoped read (OQ2).
- **The homelab** has Kata 3.32.0 on all five workers, RuntimeClass
  `kata` (overhead 250m and 130Mi, scheduling on the extension label),
  Cilium 1.20.1 and Kubernetes 1.36.5. Its `boopd.hcl` has profiles but
  no `profile_rule` blocks.

## Implementation Phases

Each phase ends with `just lint`, `just test` and its own checks green,
and one commit per numbered task. The PR split is OQ1.

### Phase 1: Verify the platform assumptions

#### Tasks

- [ ] 1.1 Record the forced-disable result above in a test fixture:
  `internal/renovate/testdata/renovate-44-forced-floor.log`, the
  `dryRun: extract` run over `boop-fixture-python-sdist` with the forced
  object, sanitized like the 4.5 fixtures, with the command in the
  testdata README.
- [ ] 1.2 **Deferred, human required.** In the homelab, run a pod with
  the boopd Job's exact posture (`internal/jobspec` output for a
  sandboxed profile, applied by hand) under `runtimeClassName: kata`:
  - it schedules on a Kata worker and `uname -r` differs from the node's;
  - Renovate 44-full starts with the read-only root filesystem and runs
    `pip-compile`, `bundler` and `./gradlew` from the fixtures;
  - the same pod with `hostUsers: false` is rejected (records the
    Finding);
  - the time from creation to Running, for the pending timeout (OQ9).
- [ ] 1.3 **Deferred, human required.** A `CiliumNetworkPolicy` with
  `toFQDNs` and the DNS proxy, selecting a Kata pod, allows
  `api.github.com` on 443 and denies `example.com`, the node address,
  the Kubernetes API and `169.254.169.254`; Hubble shows the denials.
- [ ] 1.4 Mint a token with the narrowed permissions (OQ5's set) for a
  fixture repository and run Renovate live against it with the pinned
  image: it clones, pushes a branch, opens a PR and updates the
  dashboard; a change under `.github/workflows/` is refused. Record
  which permission each Renovate step needed.

#### Success Criteria

- The forced-disable fixture is committed and its test passes.
- 1.2–1.4 results are written into this document's Findings, with any
  change they force to DESIGN-0005 made there first.

### Phase 2: The floor list and the Renovate drift test

#### Tasks

- [ ] 2.1 `internal/renovate/unsafe`: `Floor` (the ungated managers:
  `maven-wrapper`, `pip-compile`, `pipenv`, `poetry`, `pep621`, `bundler`,
  `cocoapods`, `mix`, `nuget`, `conan`), `Gated` (Renovate's `allowedValues`), and
  `DefaultFilePatterns(manager)`; the data in a generated
  `zz_generated.go` with the image digest it came from.
- [ ] 2.2 `cmd/renovate-unsafe-gen` (or a `go:generate` program):
  extracts `dist/config/options/index.js` and each floor manager's
  `index.js` from the image named in `charts/boopd/values.yaml`, and
  writes `zz_generated.go`. `just generate-unsafe` runs it.
- [ ] 2.3 The drift test (OQ3): under the `renovate` build tag, it
  re-reads the pinned image and fails when `allowedValues`, the set of
  managers whose artifact code imports `util/exec`, or any floor
  manager's default patterns differ from `zz_generated.go`. The message
  names the new or changed entries and the decision to make.
- [ ] 2.4 CI: a `renovate-drift` job running the tagged test, required
  when `charts/boopd/values.yaml` or `internal/renovate/unsafe` change.
- [ ] 2.5 DESIGN-0005's upstream tracking table points at
  `internal/renovate/unsafe` as the source of truth.

#### Success Criteria

- `zz_generated.go` regenerates byte-for-byte from the pinned image.
- The drift test fails when an entry is removed from the generated
  file, and passes on the committed one.

### Phase 3: Configuration

#### Tasks

- [ ] 3.1 `internal/config/schema.go`: a top-level `sandbox` block
  (`runtime_class`, `service_account`, optional `node_selector` and
  `toleration`, `egress_allow`, `floor_mode` = `report` | `enforce`,
  `floor_add`, `token_permissions`, `pending_timeout`); profile
  attributes `sandboxed`, `unsafe_executions`, `egress_allow`.
- [ ] 3.2 Decoding into `config.Sandbox` and `config.Profile` fields,
  with defaults: `floor_mode = "report"` (DESIGN-0005 OQ13),
  `service_account = "boopd-sandbox"`, OQ5's permission set.
- [ ] 3.3 Validation with HCL positions:
  - `unsafe_executions` only on a sandboxed profile, values from
    `unsafe.Gated`;
  - a sandboxed profile with `pod.runtime_class_name`, `node_selector`
    or `toleration` is an error (the sandbox owns placement);
  - `floor_add` names known Renovate managers;
  - `sandboxed = true` with no `sandbox` block is a warning in
    `report` mode and an error in `enforce` mode;
  - `token_permissions` may not include `workflows`, `administration`
    or `secrets`.
- [ ] 3.4 `config.BuildInput` carries the sandbox and the profile's
  sandbox fields to `jobspec`.
- [ ] 3.5 Golden HCL (`internal/config/testdata/golden.hcl`) and one
  failing case per rule; `examples/boopd.hcl` gains a commented
  `sandbox` block and sandboxed `python` and `jvm` profiles;
  `docs/config.md` documents every key (the reference test keeps it
  complete).

#### Success Criteria

- `boopd config validate` accepts the example and rejects each failing
  case at the right position.
- The reference test passes with the new keys.

### Phase 4: The profile resolver

#### Tasks

- [ ] 4.1 `profiles.New` takes the floor (`unsafe.Floor` plus
  `floor_add`) and which profiles are sandboxed.
- [ ] 4.2 `Resolve` returns the profile and whether the floor raised it:
  with a floor manager, a profile below the least strict sandboxed
  profile in `order` is raised to it; rules can raise, never lower.
- [ ] 4.3 Unknown managers (empty) with a sandbox configured resolve as
  if a floor manager were present (DESIGN-0005 § Learning the managers
  safely).
- [ ] 4.4 Table tests: no floor manager; a floor manager under an
  ordinary profile; already sandboxed; no sandboxed profile defined (an
  error from `New`, caught by validation); empty managers with and
  without a sandbox; `floor_add`.

#### Success Criteria

- The resolver table passes, including every row above.
- `PlanRun`'s existing tests pass unchanged for configurations without
  a `sandbox` block.

### Phase 5: The Job builder

#### Tasks

- [ ] 5.1 `jobspec.BuildInput.Sandbox` (`*Sandbox`: runtime class,
  ServiceAccount, optional node selector and tolerations, unsafe
  executions). With it, `podTemplate` sets `runtimeClassName`, the
  placement when given, `serviceAccountName`, an ephemeral-storage
  request and limit, and the `boopd.dev/sandbox: "true"` label.
- [ ] 5.2 Sandboxed: `RENOVATE_REDIS_URL` is not set;
  `allowedUnsafeExecutions` is written into `RENOVATE_CONFIG` from the
  profile, after `ValidateOptions` (it stays forbidden as raw input).
- [ ] 5.3 Ordinary with `floor_mode = "enforce"`: `RENOVATE_CONFIG`
  gains `force` with `{ "<manager>": { enabled: false,
  managerFilePatterns: <default> } }` for every floor manager.
- [ ] 5.4 `force` joins `forbiddenOptions`: the builder owns it, so
  neither global config nor a profile can set it.
- [ ] 5.5 `ErrNeedsSandbox` when a profile has `unsafe_executions` and
  no `Sandbox` is given.
- [ ] 5.6 Tests: the env table and golden Job for an ordinary run, an
  ordinary run in `enforce` mode, a sandboxed run with and without
  dedicated placement, `force` rejected in global and profile config.

#### Success Criteria

- Golden Jobs match for all four shapes; the forced object matches the
  Phase 1.1 fixture's.
- `TestBuildEnv_RejectsForbiddenOptions` covers `force`.

### Phase 6: The narrowed token

#### Tasks

- [ ] 6.1 `platform.Minter.Mint(ctx, installationID, repoIDs, perms)`
  with `perms map[string]string` (nil keeps today's behaviour);
  `internal/platform/github` sends `permissions` in the access-token
  request.
- [ ] 6.2 `RunRenovate` passes `sandbox.token_permissions` for sandboxed
  runs; ordinary runs pass nil.
- [ ] 6.3 `test/fakegithub` records the requested permissions per token
  and rejects a permission the fake App was not granted (GitHub answers
  422).
- [ ] 6.4 A 422 on mint is a non-retryable `TokenPermissions` error with
  the missing permission named, so a misconfigured set fails loudly.

#### Success Criteria

- The mint request body carries the set for sandboxed runs only
  (`fakegithub` assertion).
- A set the App lacks fails the run with `TokenPermissions`.

### Phase 7: Floor detection in PlanRun

#### Tasks

- [ ] 7.1 `internal/platform/github`: `ListTree(ctx, repo, ref)` over
  `GET /repos/{owner}/{repo}/git/trees/{ref}?recursive=1`, returning
  paths and `truncated`.
- [ ] 7.2 `unsafe.MatchFloor(paths)`: the floor managers whose default
  patterns match any path.
- [ ] 7.3 `PlanRunInput` gains `RepoID` and `DefaultBranch`;
  `PlanRun` lists the tree (OQ4), adds matched managers to the resolve
  input, and treats `truncated` as a floor match. `RunPlan` gains
  `Sandboxed`, `FloorRaised` and `FloorManagers`.
- [ ] 7.4 A tree-listing failure is not fatal: PlanRun resolves from the
  known managers, logs at warn and counts
  `boopd_floor_detection_errors_total`.
- [ ] 7.5 `test/fakegithub` serves trees; tests for a match, no match, a
  truncated tree and an error.

#### Success Criteria

- A repository whose tree has `requirements.txt` with a pip-compile
  header file resolves sandboxed before its first run.
- The REST spend per run grows by exactly one call (fake counter).

### Phase 8: RunRenovate, RepoWorkflow and failing closed

#### Tasks

- [ ] 8.1 `RunRenovate`: for a sandboxed plan, check the sandbox (the
  RuntimeClass exists, OQ2) before minting; when it does not, or no
  `sandbox` block is configured, return the non-retryable
  `NeedsSandbox` error without creating anything.
- [ ] 8.2 A sandboxed Job that stays unschedulable past
  `sandbox.pending_timeout` (OQ9) is deleted and classified
  `NeedsSandbox`, not `Pending`, when its pod events show the
  RuntimeClass or placement as the cause.
- [ ] 8.3 `RepoWorkflow`, behind a new `GetVersion` change
  (`sandbox-1`): on `NeedsSandbox`, phase `needs-sandbox`, a
  `repository_event` log line (OQ6), and a wait until the next cadence;
  `recheck` retries at once.
- [ ] 8.4 In `report` mode, a floor-raised plan runs as an ordinary Job
  (today's behaviour, OQ7) and logs `would_sandbox=true` with the floor
  managers.
- [ ] 8.5 `RunResult.Sandbox` and `RunResult.UnsafeExecutions`; the
  `Sandboxed` (Bool) search attribute registered by `boopd worker` and
  upserted from the plan.
- [ ] 8.6 Replay: record a `RepoWorkflow` history from the current code
  before 8.3 and keep `TestReplay_Histories` green; add a history for a
  `needs-sandbox` run.

#### Success Criteria

- The workflow test suite covers sandboxed success, `NeedsSandbox`, the
  wait and a recheck, and `report` mode.
- Every committed replay history still replays.

### Phase 9: Observability

#### Tasks

- [ ] 9.1 `boopd_runs_total` and the run duration histograms gain a
  `sandboxed` label.
- [ ] 9.2 `boopd_sandbox_ready` (gauge, 1 when the RuntimeClass exists),
  refreshed by the worker every minute; it does not affect `/readyz`,
  so a missing sandbox never stops ordinary runs.
- [ ] 9.3 `boopd_runs_needs_sandbox_total{app}` and
  `boopd_floor_raised_total{app, manager, mode}`.
- [ ] 9.4 The run's log fields include `sandbox`, `unsafe_executions`,
  `floor_managers` and `would_sandbox`.

#### Success Criteria

- The metric set test lists every new instrument with its labels.

### Phase 10: Chart

#### Tasks

- [ ] 10.1 Values and schema: `sandbox.enabled`, `runtimeClass`,
  `nodeSelector`, `tolerations`, `egressAllow`, `floorMode`,
  `floorAdd`, `tokenPermissions`, `pendingTimeout`,
  `serviceAccount.create`; `networkPolicies.provider` (`cilium`,
  `kubernetes`, `vpc-cni`), `sandbox.egress.denyCIDRs` and
  `allowCIDRs`; rendered into `boopd.hcl`.
- [ ] 10.2 `templates/sandbox-serviceaccount.yaml`: `boopd-sandbox`,
  `automountServiceAccountToken: false`, no bindings.
- [ ] 10.3 `templates/sandbox-networkpolicy.yaml`, per sandboxed
  profile:
  - `cilium`: a `CiliumNetworkPolicy` with no ingress, DNS through the
    DNS proxy to `kube-dns`, and 443 to `toFQDNs` (sandbox plus profile
    `egress_allow`); rendered only when the API version exists;
  - `kubernetes`: a `NetworkPolicy` with DNS to `kube-dns` and 443 to
    `0.0.0.0/0` except `denyCIDRs` (private, link-local, CGNAT and the
    configured cluster CIDRs), narrowed by `allowCIDRs`;
  - `vpc-cni`: the `kubernetes` objects plus `169.254.169.254/32` and
    the VPC CIDR (a required value) in the deny list.
- [ ] 10.4 RBAC for the RuntimeClass check (OQ2): a ClusterRole with
  `get` on `runtimeclasses.node.k8s.io`, limited by `resourceNames` to
  the configured class, and its binding.
- [ ] 10.5 The runs quota adds the RuntimeClass overhead
  (`sandbox.overhead`, default 250m and 130Mi) for sandboxed Jobs.
- [ ] 10.6 `PrometheusRule`: `BoopdSandboxNotReady`,
  `BoopdRunsNeedSandbox`, and `BoopdSandboxEgressDenied` (Hubble
  metrics, `cilium` only).
- [ ] 10.7 `NOTES.txt`: the `kubernetes` and `vpc-cni` warning that host
  names are not filtered; the `report` mode notice.
- [ ] 10.8 helm-unittest suites for every template and provider, the
  schema's rejections, and `boopd config validate` on the rendered
  config.

#### Success Criteria

- `just helm-test` passes, with a snapshot per provider.
- The rendered config validates in `report` and `enforce` mode.

### Phase 11: End to end

#### Tasks

- [ ] 11.1 The k3d harness registers RuntimeClass `kata` with handler
  `runc` (OQ10) and, for the dedicated mode, labels and taints one k3d
  agent.
- [ ] 11.2 `TestChart_Sandbox_Run`: a repository whose fake tree has a
  floor file runs sandboxed: runtime class, ServiceAccount, no
  `RENOVATE_REDIS_URL`, `allowedUnsafeExecutions` in the stub's
  `RENOVATE_CONFIG`, narrowed token permissions at the fake.
- [ ] 11.3 `TestChart_Sandbox_Dedicated`: the same with placement; the
  pod lands on the tainted agent.
- [ ] 11.4 `TestChart_Sandbox_FailClosed`: with the RuntimeClass
  deleted, the run ends `needs-sandbox`, no Job is created, and the
  metric counts it; restoring it and a recheck runs.
- [ ] 11.5 `TestChart_Floor_Enforce`: an ordinary run's `RENOVATE_CONFIG`
  carries the forced floor object; in `report` mode it does not.
- [ ] 11.6 The stub Renovate image prints its `RENOVATE_CONFIG` keys and
  pod facts the tests read (runtime class from the downward API).

#### Success Criteria

- `just e2e` and CI's E2E job pass with the new scenarios.

### Phase 12: Documentation

#### Tasks

- [ ] 12.1 `docs/sandbox.md`: what the sandbox is for, prerequisites
  (Kata and its RuntimeClass; Cilium, standard NetworkPolicy or VPC
  CNI), the `boopd.hcl` and chart settings, the token and default-branch
  protection, `report` to `enforce`, troubleshooting `needs-sandbox`.
- [ ] 12.2 `docs/config.md` and the chart README (helm-docs).
- [ ] 12.3 CLAUDE.md: the layout line for `internal/renovate/unsafe`,
  and the "Decisions already made" entry for the sandboxed tier.
- [ ] 12.4 DESIGN-0005 → Approved at the start of Phase 2; →
  Implemented in Phase 13.

#### Success Criteria

- markdownlint passes; the config reference test passes.

### Phase 13: Releases and the homelab

#### Tasks

- [ ] 13.1 The PRs (OQ1), each with its label.
- [ ] 13.2 **Deferred, human required.** Homelab config: add the
  `profile_rule` blocks; a `sandbox` block on RuntimeClass `kata` with
  `networkPolicies.provider = "cilium"`; sandboxed `python` and `jvm`
  (`unsafe_executions = ["gradleWrapper"]`) profiles; `floor_mode =
  "report"`.
- [ ] 13.3 **Deferred, human required.** A week in `report` mode: read
  `boopd_floor_raised_total` and the `would_sandbox` lines; confirm the
  list of repositories that will move.
- [ ] 13.4 **Deferred, human required.** Switch to `enforce`; run
  DESIGN-0005 § Testing Strategy's homelab checks over the IMPL-0001
  8.1 fixtures; record them for IMPL-0001 8.8 in the spike results
  investigation.
- [ ] 13.5 The chart default moves to `enforce` in the release after
  13.4 (DESIGN-0005 OQ13).
- [ ] 13.6 IMPL status → Completed; DESIGN-0005 → Implemented.

#### Success Criteria

- The homelab runs `enforce` with every floor repository sandboxed and
  no `needs-sandbox` outside the fixtures' failure cases.
- IMPL-0001 8.8's containment checks pass and are recorded.

## File Changes

| Path | Phase | Change |
| - | - | - |
| `internal/renovate/testdata/renovate-44-forced-floor.log`, README | 1 | fixture |
| `internal/renovate/unsafe/*`, `cmd/renovate-unsafe-gen` | 2 | floor, gated list, patterns, generator, drift test |
| `.github/workflows/ci.yml`, `justfile` | 2 | `renovate-drift` job, `generate-unsafe` |
| `internal/config/{schema,config,validate,jobinput,profiles}.go`, testdata | 3 | `sandbox` block, profile fields, validation |
| `examples/boopd.hcl`, `docs/config.md` | 3, 12 | example and reference |
| `internal/profiles/profiles.go` | 4 | floor and raise |
| `internal/jobspec/{job,env}.go` | 5 | sandboxed pod, forced floor, `force` forbidden |
| `internal/platform/platform.go`, `internal/platform/github/{app,tree}.go` | 6, 7 | mint permissions, tree listing |
| `test/fakegithub/fakegithub.go` | 6, 7 | permissions, trees |
| `internal/activities/{plan,run,classify,metrics}.go` | 7, 8, 9 | detection, sandbox check, `NeedsSandbox` |
| `internal/workflows/{repo,run_types,search,names}.go`, testdata/replay | 8 | phase, fields, search attribute, version |
| `internal/worker/worker.go`, `internal/observability/metrics.go` | 8, 9 | gauge, metrics, search attribute registration |
| `charts/boopd/{values.yaml,values.schema.json,templates/*,tests/*}` | 10 | sandbox objects |
| `test/e2e/*`, `test/stub-renovate/*` | 11 | scenarios |
| `docs/sandbox.md`, CLAUDE.md, DESIGN-0005 | 12, 13 | docs, status |

## Testing Plan

- **e2e in k3d is the main gate** (Phase 11): the sandboxed run, both
  placement modes, failing closed, and the forced floor object, against
  the real chart with a `runc`-backed `kata` RuntimeClass, the stub
  Renovate and the fake GitHub.
- **Real Renovate behaviour is pinned by fixtures:** the forced-disable
  log (Phase 1.1) and the drift test over the pinned image (Phase 2).
- **Unit tests** for the resolver table, config validation, the builder's
  golden Jobs, tree matching and the mint body.
- **Workflow tests** in the Temporal `testsuite` for the new branch, and
  replay over the committed histories.
- **The isolation itself is proven in the homelab** (Phases 1 and 13),
  since k3d has no Kata.

## Dependencies

- DESIGN-0005 (all questions resolved) and #33 (`allowedUnsafeExecutions`
  refused).
- The homelab: Kata RuntimeClass `kata`, Cilium 1.20 with the DNS proxy
  and Hubble metrics, and the IMPL-0001 8.1 fixtures.
- Docker in CI for the drift test.
- GitHub App permissions that cover OQ5's set (they do today; the
  narrowed set is a subset).

## Open Questions

Each question lists options; **a** is the recommendation and the phases
above are written to it.

### OQ1: How is the work split into PRs?

- **a** (recommended): three PRs. A: Phases 1–5 (fixture, floor, drift
  test, config, resolver, builder), `minor`. B: Phases 6–12 (token,
  detection, workflows, observability, chart, e2e, docs), released in
  `report` mode, `minor`. C: the `enforce` default (13.5), `minor`.
- **b**: one PR for everything, then the default flip.
- **c**: one PR per phase.
- **other:**

### OQ2: How does the worker know the sandbox RuntimeClass exists?

- **a** (recommended): a ClusterRole with `get` on
  `runtimeclasses.node.k8s.io`, limited by `resourceNames` to the
  configured class; checked before each sandboxed run and every minute
  for the gauge.
- **b**: no API check; create the Job and treat the pod admission
  failure (`FailedCreate`) or the pending timeout as `NeedsSandbox`.
  No cluster-scoped RBAC, but slower and noisier failures.
- **c**: trust the config: `sandbox.enabled` means it exists.
- **other:**

### OQ3: Where does the Renovate drift test run?

- **a** (recommended): a Go test under a `renovate` build tag that
  extracts the chart's pinned image with Docker, in a CI job required
  when the pinned image or `internal/renovate/unsafe` change, and in
  `just test-renovate` locally.
- **b**: only the generator, run by hand on Renovate upgrades; CI checks
  the generated file is committed.
- **c**: a scheduled CI job against the latest Renovate image, to warn
  before the pin moves.
- **other:**

### OQ4: When does PlanRun look for floor files?

- **a** (recommended): before every run: one REST tree call per run,
  which also catches a floor file added between runs.
- **b**: only in `CheckRepo` (discovery passes and config pushes), and
  rely on the forced disable between them.
- **c**: never; only reports of sandboxed runs and profile rules.
- **other:**

### OQ5: Which permissions does the default sandbox token set carry?

- **a** (recommended): `contents`, `pull_requests` and `issues` write;
  `checks`, `statuses`, `metadata` and `vulnerability_alerts` read (the
  shared preset turns on `vulnerabilityAlerts`); configurable as
  `sandbox.token_permissions`, never with `workflows`,
  `administration` or `secrets`. Phase 1.4 confirms it.
- **b**: the same without `vulnerability_alerts`; security PRs come only
  from ordinary repositories.
- **c**: a fixed set, not configurable.
- **other:**

### OQ6: Where does the `needs-sandbox` repository event go before the store exists?

- **a** (recommended): a structured `repository_event` log line, the
  metric, the `Phase` search attribute and the alert; IMPL-0005's store
  records it as an event when it lands.
- **b**: wait for IMPL-0005 and ship only the metric and phase now.
- **other:**

### OQ7: What does `report` mode do to a floor repository?

- **a** (recommended): it runs as an ordinary Job exactly as today (no
  forced disable), with `would_sandbox` logged and counted, so the first
  release changes nothing.
- **b**: it already forces the floor managers off in ordinary runs, and
  only `needs-sandbox` waits for `enforce`; Python and Ruby updates stop
  wherever no sandbox is configured.
- **other:**

### OQ8: User namespaces for ordinary Jobs?

Kubernetes 1.36 has `hostUsers: false` GA and the homelab has it on;
ordinary Jobs run runc, which supports it. It is outside DESIGN-0005.

- **a** (recommended): a follow-up issue, not in this IMPL.
- **b**: add it here as an opt-in `runs.host_users = false` setting,
  default off.
- **c**: add it here, on by default.
- **other:**

### OQ9: Pending timeout for sandboxed Jobs?

- **a** (recommended): its own `sandbox.pending_timeout`, defaulting to
  the runs pending timeout plus 60 s for the VM boot, tuned by Phase
  1.2's measurement.
- **b**: the runs pending timeout for both.
- **other:**

### OQ10: How does e2e stand in for Kata?

- **a** (recommended): RuntimeClass `kata` with handler `runc` in k3d;
  the isolation itself is a homelab check.
- **b**: skip the sandbox e2e scenarios; unit and helm tests only.
- **c**: a CI runner with nested virtualisation running real Kata in
  k3d.
- **other:**

### OQ11: Where do the floor managers' default patterns come from at build time?

- **a** (recommended): generated into `internal/renovate/unsafe` from
  the pinned image (Phase 2), so the builder needs no image at run
  time.
- **b**: read from the Renovate image at worker start through a helper
  Job.
- **c**: hand-maintained in Go, checked by the drift test.
- **other:**

<!--docz:references:start-->
## References

- DESIGN-0005 (the design; OQ answers 2026-10-25)
- DESIGN-0001 § Process builder; ADR-0009
- IMPL-0001 8.1 (fixtures), 8.8 (re-scoped)
- IMPL-0004 Phase 1 (replay histories), Phase 10 (chart patterns)
- INV-0003 (Kata VMM), INV-0004 (nix)
- #33 (`allowedUnsafeExecutions` refused)
- Renovate 44.149.2 (`renovate:44-full`, pinned) and 44.179.2
<!--docz:references:end-->
