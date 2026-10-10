# CLAUDE.md

Per-repo orientation for `donaldgifford/boop`.

## What this is

`boop` runs Renovate per repository with Temporal as the control plane.
The service is `boopd`. It is a separate product from
`donaldgifford/renovate-operator` (the kubebuilder/CRD option) and does
not have to match it.

**Read [INV-0001](docs/investigation/0001-temporal-as-the-renovate-control-plane.md)
first**, starting with its "Handoff: starting boop" section. It is the
founding document: workflow shapes, execution model, spike scope and
success criteria, and the Renovate behaviours the process builder must
reproduce.

### Decisions already made

Do not re-open these without a new document that says why:

- Temporal is the control plane. No hand-built queue, scheduler or
  leader election.
- One `RepoWorkflow` per repository (ID `repo/<platform>/<repo id>`),
  one `InstallationWorkflow` per installation, a `DiscoveryWorkflow` on a
  Temporal Schedule.
- Execution: each run is a Kubernetes Job created by the `RunRenovate`
  activity from the upstream Renovate image (ADR-0009, superseding
  ADR-0003's worker pool). The Job pod holds one repository-scoped token
  and nothing else; the worker holds the App key and a namespaced Role for
  Jobs, Pods, logs and Secrets. An ecosystem profile (config, not a CRD)
  picks the pod overlay and Renovate overrides per repository; Python gets
  the strict one (DESIGN-0001).
- No CRDs. Config is an HCL file shipped with the chart, decoded with
  `hclkit` (`github.com/donaldgifford/hclkit` today, `x/hclkit` after
  ADR-0007's Phase 0) (IMPL-0001 OQ2).
- Tests lean on end-to-end runs: a k3d cluster locally (`just e2e`) and
  in CI, with stub Renovate and GitHub images. Mocks and unit tests cover
  what e2e cannot reach cheaply (IMPL-0001 OQ3).
- A repository is onboarded only if it contains the Renovate config file,
  which repo-guardian writes. Renovate's own onboarding is off.
- Postgres store behind an HTTP API and UI, but not in the spike. Where
  each piece of data lives is decided in the DESIGN after the spike.
- Naming: repository `boop`, service `boopd` (binary, image, chart,
  Temporal namespace), bot `boop-bot` (GitHub App).
  `boop-bot` is used by `boopd` only, so `/rate_limit` readings reflect
  only its spend.
- GitHub only (github.com, App auth). No Forgejo; renovate-operator
  covers it.
- Scaling (ADR-0008):
  - workflows scale with the repository count;
  - concurrent runs are capped by each installation's budget, discovered
    from `/rate_limit` for REST and GraphQL and never configured;
  - admitted runs are Jobs; nothing else scales. The `boopd` worker runs a
    fixed, small replica count (ADR-0008 as amended by ADR-0009).
- Shared code goes to `donaldgifford/x` (one Go module, versioned
  together) in Phase 0, after the spike. The spike copies.

### Spike status

The checklist is [IMPL-0001](docs/impl/0001-boopd-spike-temporal-control-plane-job-per-run-discovery-and.md):
phases with tasks, success criteria and the implementation open questions.
Check tasks off there as they land. The summary below tracks INV-0001
§ "First steps, in order":

1. Repo, module, tooling — done (Go 1.27.2).
2. Investigation in `docs/`, this file — done.
3. `internal/platform` copied from renovate-operator `0183661` — done.
   Changes: Forgejo client dropped, lint fixes (`%w: %w` wrapping,
   formatting).
4. Process builder — done: `internal/jobspec` ported from renovate-operator
   `0183661` (one repository per Job, profile overlay, owned token Secret)
   with the env-table behaviour tests.
5. Temporal plumbing and budget entity — done: `internal/temporal`
   (config, mTLS/OIDC, dial, worker versioning, schedules, SDK metric
   views, dev-server test helper), `internal/workflows` (names, priority
   presets, `InstallationWorkflow`) and `internal/activities`
   (`AcquireBudget`) ported from repo-guardian `feat/impl-0028-controls-foundations` @ `d1f20a0`.
   Changes: one task queue and worker deployment (`boopd`); the budget is
   discovered from `/rate_limit` by a `ReadRateLimit` activity the entity
   runs itself, tracks `core` and `graphql`, counts open leases against
   the per-resource EWMA, caps concurrent runs and honours a
   secondary-limit `retryAt` (DESIGN-0001 § InstallationWorkflow).
6. Activities — done (IMPL-0001 Phase 5): `ListInstallations`,
   `DiscoverInstallation`, `CheckRepo`, `ReadRateLimit`, `RunRenovate`
   with classification, run metrics; e2e in k3d against
   `test/fakegithub`. Workflows and the worker role — done (Phase 6):
   `RepoWorkflow` (convergence table, absence check, ContinueAsNew),
   `DiscoveryWorkflow` on per-App schedules, search attributes,
   `internal/observability` (slog, Prometheus exporter, health) and
   `boopd worker`, with the worker e2e.
7. Chart (Role, profiles, PodSecurity labels) and homelab deploy. No
   custom Renovate image; Jobs run the upstream one.
8. Run the success criteria; record results in a new investigation.

Sibling checkouts used as sources: `~/code/renovate-operator`,
`~/code/repo-guardian` (`feat/impl-0028-controls-foundations`), `~/code/x`.

### Scaffold note

The rest of this file, along with the Dockerfile and the chart, comes
from the generic service template. The distroless image and the
`LISTEN_ADDR`/probe contract fit the worker and API roles. Renovate runs
use the upstream image in Jobs (ADR-0009), so the image stays; the chart
gains RBAC and profiles in step 7.

The container image and the chart are published together as OCI
artifacts on every release.

- Service entrypoint under `cmd/boopd/`; library code under
  `internal/` (private to the module).
- Built into a distroless container via `docker buildx bake`
  (`docker-bake.hcl` defines the local / ci / release targets).
- Helm chart in `charts/boopd/` with helm-unittest suites in
  `tests/` — the chart is the deployment contract, not an afterthought.

## Layout

```text
cmd/boopd/                # main package: `boopd worker`, `boopd config validate`
internal/                 # library code; not importable outside this module
internal/platform/        # GitHub discovery, config probe, token minting
internal/temporal/        # Temporal client config, worker, versioning, schedules
internal/workflows/       # deterministic workflow code; InstallationWorkflow budget entity
internal/activities/      # side effects, registered by name: discovery, CheckRepo, ReadRateLimit, AcquireBudget, RunRenovate
internal/observability/   # slog logger, OTel meter provider + Prometheus exporter, boopd metric set, /healthz and /readyz
internal/worker/          # worker role: search attributes, register, start, promote, schedules, readiness, graceful stop
internal/jobspec/         # Job + env builder for one Renovate run (ported from renovate-operator)
internal/config/          # HCL config file via hclkit: decode, defaults, validation, secrets, BuildInput
internal/profiles/        # pure profile resolver: extends + managers -> strictest profile
internal/kube/            # Job lifecycle Runner: suspended create, Secret, unsuspend, log follow, exit, delete
internal/renovate/        # log scanner + report parser: progress, Repository finished, update tuples
test/stub-renovate/       # stub Renovate image for e2e (bake target stub-renovate; never pushed)
test/e2e/                 # k3d e2e suite, build tag e2e; `just e2e`
test/fakegithub/          # in-process GitHub (App, mint/revoke, paging, probes, /rate_limit) for tests
examples/boopd.hcl        # the design's example config; `boopd config validate` keeps it loadable
docs/investigation/       # INV-0001 is the founding document
charts/boopd/   # Helm chart + unittest suites + values.schema.json
Dockerfile                # multi-stage distroless build (VERSION/COMMIT/DATE args)
docker-bake.hcl           # bake targets: default (local), ci, release
justfile                  # task runner; imports docker.just + helm.just
mise.toml                 # pinned toolchain: go, golangci-lint, helm, ct, k3d, ...
.github/workflows/        # ci.yml + release.yml (per-registry train)
```

## Workflows

- `just check` — lint + test (pre-commit gate)
- `just e2e` — create the k3d cluster, build and import the stub
  Renovate image, run `go test -tags e2e ./test/e2e/...` against it
  (`just e2e-down` deletes the cluster); CI's "E2E Tests" job does the same
- `just build` — binary into `build/bin/boopd`
- `just docker-build` — host-native image via bake
- `just helm-test` — chart lint (helm + ct) and helm-unittest suites
- `just helm-docs` — regenerate the chart README from README.md.gotmpl
- `just k3d-install` — build dev image → import into local k3d cluster
  → `helm upgrade --install` (the inner dev loop; `just k3d-down` to
  tear down)

## Configuration contract

The chart manages the container environment — the service must honor:

- `LISTEN_ADDR` / `METRICS_ADDR` / `LOG_LEVEL` (from `config.*` values)
  and `POD_NAME` (injected from the pod spec).
- `configMap.data` / `secrets.stringData` arrive via `envFrom`;
  `extraEnv` appends raw entries. Colliding with the chart-managed
  names fails the render (`validateEnvCollisions` helper).
- Probes hit `/healthz` and `/readyz` on the listen port.

## Releases

Releases happen on merge to `main`, not on manual tags, and versions
are **lockstep** — binary, image, and chart all carry the same tag:

- The merged PR's semver label (`major`/`minor`/`patch`, or
  `dont-release` to skip everything) drives `bump-version`, which
  creates and pushes the tag.
- goreleaser publishes the GitHub Release (binary archives, checksums,
  SBOMs); bake builds the multi-arch image (cosign + SLSA);
  `VERSION`/`COMMIT`/`DATE` are stamped into both.
- The chart publishes at the tag-derived version
  (`helm package --version <tag> --app-version <tag>`); `Chart.yaml`
  keeps a `0.0.0-dev` placeholder and is never bumped by hand.
- Chart-only changes ship with the next release — merge them with a
  `patch` label if they need to go out on their own.

Do NOT push tags by hand — the release train owns them.

## Conventions

- Conventional Commits; changelogs are git-cliff-generated (root
  `cliff.toml` for the repo, `charts/boopd/cliff.toml` for
  the chart-only changelog).
- Lint gates: `golangci-lint` (config in `.golangci.yml`), `yamllint`,
  `markdownlint-cli2`, `actionlint`. Run `just --list` for the menu.
