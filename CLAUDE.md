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
- Execution option A: a worker pool that execs the Renovate CLI, one
  repository per activity, one activity slot per pod. Fallback is a
  Kubernetes Job per repository if the isolation criteria fail.
- No CRDs. Config is a file shipped with the chart.
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
  - workers follow admitted runs, at a fixed replica count in the spike.

  The scaling mechanism (KEDA trigger, Deployment or Job per run) is a fast
  follow.
- Shared code goes to `donaldgifford/x` (one Go module, versioned
  together) in Phase 0, after the spike. The spike copies.

### Spike status

Tracks INV-0001 § "First steps, in order":

1. Repo, module, tooling — done (Go 1.27.1).
2. Investigation in `docs/`, this file — done.
3. `internal/platform` copied from renovate-operator `0183661` — done.
   Changes: Forgejo client dropped, lint fixes (`%w: %w` wrapping,
   formatting).
4. Process builder + behaviour tests — next.
5. Temporal plumbing and budget entity from repo-guardian `v2` @ `278c7ec`.
6. `RunRenovate`, `RepoWorkflow`, `InstallationWorkflow`,
   `DiscoveryWorkflow`.
7. Worker image (Renovate full image + Go binary) and homelab deploy.
8. Run the success criteria; record results in a new investigation.

Sibling checkouts used as sources: `~/code/renovate-operator`,
`~/code/repo-guardian` (`v2` branch), `~/code/x`.

### Scaffold note

The rest of this file, along with the Dockerfile and the chart, comes
from the generic service template. The distroless image and the
`LISTEN_ADDR`/probe contract fit the API role. The spike's worker needs
a Renovate-based image instead (INV-0001 § Spike), so expect the image
and chart to change in step 7.

The container image and the chart are published together as OCI
artifacts on every release.

- Service entrypoint under `cmd/boop/`; library code under
  `internal/` (private to the module).
- Built into a distroless container via `docker buildx bake`
  (`docker-bake.hcl` defines the local / ci / release targets).
- Helm chart in `charts/boop/` with helm-unittest suites in
  `tests/` — the chart is the deployment contract, not an afterthought.

## Layout

```text
cmd/boop/      # main package — keep thin, call into internal/
internal/                 # library code; not importable outside this module
internal/platform/        # GitHub discovery, config probe, token minting
docs/investigation/       # INV-0001 is the founding document
charts/boop/   # Helm chart + unittest suites + values.schema.json
Dockerfile                # multi-stage distroless build (VERSION/COMMIT/DATE args)
docker-bake.hcl           # bake targets: default (local), ci, release
justfile                  # task runner; imports docker.just + helm.just
mise.toml                 # pinned toolchain: go, golangci-lint, helm, ct, k3d, ...
.github/workflows/        # ci.yml + release.yml (per-registry train)
```

## Workflows

- `just check` — lint + test (pre-commit gate)
- `just build` — binary into `build/bin/boop`
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
  `cliff.toml` for the repo, `charts/boop/cliff.toml` for
  the chart-only changelog).
- Lint gates: `golangci-lint` (config in `.golangci.yml`), `yamllint`,
  `markdownlint-cli2`, `actionlint`. Run `just --list` for the menu.
