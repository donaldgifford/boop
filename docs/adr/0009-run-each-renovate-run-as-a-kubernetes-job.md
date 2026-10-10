---
id: ADR-0009
title: "Run each Renovate run as a Kubernetes Job"
status: Accepted
author: Donald Gifford
created: 2026-10-07
---

<!-- markdownlint-disable-file MD025 MD041 -->

# ADR-0009: Run each Renovate run as a Kubernetes Job

<!--toc:start-->
- [Summary](#summary)
- [Context](#context)
- [Decision](#decision)
- [Consequences](#consequences)
  - [Positive](#positive)
  - [Negative](#negative)
  - [Neutral](#neutral)
- [Alternatives Considered](#alternatives-considered)
- [References](#references)
<!--toc:end-->

## Summary

Every Renovate run is a Kubernetes Job that the `RunRenovate` activity
creates, follows and deletes. The Job runs the upstream Renovate image with a
pod spec and Renovate overrides chosen by the repository's **ecosystem
profile**, and it holds one repository-scoped token and nothing else.
Temporal stays the control plane: `RepoWorkflow` owns cadence, admission,
retries, convergence and the steps after the run. This supersedes the worker
pool in ADR-0003 (option A). ADR-0003's isolation rules carry over where they
still apply; its execution shape does not.

## Context

ADR-0003 chose a worker pool that execs the Renovate CLI, with a Job per
repository (option B) as the fallback "if the isolation criteria fail".
Writing DESIGN-0001 showed that they fail by construction, not by
measurement:

- A package manager that Renovate runs executes as the same UID as the
  worker, in the same container. It can read every mounted Secret and the
  environment of any process of that UID. `exposeAllEnv=false` filters only
  the environment a child *inherits*. So anything the worker holds, the
  repository's dependency graph can read.
- The worker must hold a Temporal client certificate to poll. With it, a
  child can poll the task queue as a worker and receive other runs' inputs.
  So the credential that fetches work cannot share a container with
  Renovate, whatever is done about the App key.
- The only fixes inside option A are a second container (a `runner` sidecar
  holding the certificate and key, a socket protocol to a credential-free
  executor, a PID 1 kill sweep between runs) or a second pod. The sidecar
  version costs a third role, a custom image and about 300 lines of
  protocol, and still shares one long-lived pod across repositories.

Whether that boundary matters depends on what runs inside the container. The
fleet's shared presets (`boop-bot/renovate-config`) settle it:

| Ecosystem in the fleet | What a lockfile update executes | Third-party code |
| ---------------------- | ------------------------------- | ---------------- |
| python: `pip-compile`, `poetry`, `pipenv`, `pep621` (uv, pdm, hatch) | builds any dependency without a wheel from sdist, which runs its `setup.py` or build backend | **yes, on every update** |
| node: `npm`, `bun` (also yarn, pnpm) | lockfile-only install with scripts ignored (`ignoreScripts`, `allowScripts=false`) | no, by Renovate's defaults |
| go, cargo, helm, terraform, terragrunt | download and resolve | no |
| nix | `nix flake lock` evaluates the inputs' expressions | no process execution |
| hugo (`git-submodules`) | fetches third-party repositories | no execution |
| kustomize, kubernetes, argocd, docker, GitHub Actions, mise, devcontainer, homebrew, tflint, typst, tool-versions, bazel | file edits and datasource lookups | no |

The fleet runs third-party build code today, on every Python lockfile
update, and only Python needs the strongest posture. A pod per run gives the
boundary for free and, because the pod spec is built per run, lets that
posture differ by ecosystem.

Two more facts weighed in. renovate-operator already runs Renovate as a Job
with a PodSecurity "restricted" pod spec; its `internal/jobspec` builder is
copyable, the way `internal/platform` was. And with a pool, scaling the pool
is a problem of its own (ADR-0008): KEDA with the Temporal trigger needs
mTLS or an API key, which is the pain repo-guardian is in now. With a Job per
run there is nothing to scale: admitted runs *are* the Jobs.

## Decision

- **One Job per run, created by the activity.** `RunRenovate`, running in
  the `boopd` worker, mints a token scoped to the run's repository, builds
  the Job from the repository's profile and the process builder, creates it
  suspended, creates a token Secret owned by the Job, unsuspends it, follows
  the pod log for progress and the report, waits for the container to exit,
  revokes the token and deletes the Job. Cancellation and the soft deadline
  delete the Job.
- **The Job pod holds one token and nothing else.** Upstream Renovate image
  pinned by digest; no service account token; no service links; the token
  arrives through a `secretKeyRef` from the per-run Secret and is in
  Renovate's environment only; `restartPolicy: Never`, `backoffLimit: 0`,
  `activeDeadlineSeconds` and `ttlSecondsAfterFinished` as safety nets;
  PodSecurity "restricted" settings copied from renovate-operator.
- **Ecosystem profiles choose the posture.** A profile is a named pod
  overlay (runtime class, resources, labels that select a NetworkPolicy,
  node selection) plus Renovate global overrides (`customEnvVariables`,
  constraints). Profiles are ordered by strictness. A repository gets the
  strictest profile that any of its ecosystems maps to; a repository whose
  ecosystems are unknown gets the strictest profile there is. Profiles are
  config shipped with the chart (ADR-0004), not CRDs. How ecosystems are
  learned is DESIGN-0001 OQ1.
- **The worker gets a namespaced Role** for Jobs (create, get, list, watch,
  delete), Pods (get, list, watch), pod logs (get) and Secrets (create, get,
  delete; no list). Nothing cluster-scoped.
- **Temporal stays the control plane**, on one task queue. Nothing about
  `RepoWorkflow`, `InstallationWorkflow`, `DiscoveryWorkflow` or the budget
  changes except that the activity's "queued" state becomes the pod's
  Pending state.
- **ADR-0003's isolation rules carry over**: toolchains baked into the
  image, `binarySource=global`, the datasource cache in Redis, fresh
  directories (a fresh pod now), script controls at their defaults, the
  token only in the environment and never on disk, revoked at run end.
  ADR-0003 is superseded.
- **ADR-0008 is amended**: its third layer, "workers follow admitted runs",
  becomes "Jobs are admitted runs". No scaler is needed. A cluster-capacity
  cap is DESIGN-0001 OQ12. The `boopd` worker Deployment runs a fixed, small
  replica count, which is enough for thousands of mostly sleeping workflows.

## Consequences

### Positive

- Isolation is per pod, the boundary renovate-operator already provides,
  and nothing but one scoped token is ever within reach of repository code.
- Per-ecosystem hardening is a pod-spec and Renovate-config overlay, not
  code. Python can refuse sdist builds and run under a sandboxing runtime
  class while Go and Helm run the baseline.
- No sidecar, no local protocol, no custom Renovate image, no KEDA.
- The process builder is a copy of renovate-operator `internal/jobspec`
  (INV-0001 step 4 gets shorter).
- Cancellation, the soft deadline and worker shutdown all reduce to deleting
  a Job.

### Negative

- The worker needs write access to the cluster, namespaced.
- A pod start per run: scheduling plus image pull when the node has no
  cached image. Seconds at this fleet's size; measured in the spike.
- The token exists briefly as a Secret object. Mitigated by scoping, by
  ownership (deleted with the Job) and by revocation at run end.
- The report arrives as one log line (`reportType: logging`). The kubelet
  reassembles the runtime's partial-line splits when serving `pods/log`,
  but a very large report is a very large line; the log-event fallback
  covers a missing or truncated report.
- About four API-server writes per run and a Job object per run in flight.
- One long-running activity per run occupies a worker slot for up to an
  hour; slots are goroutines, and the slot count is sized to the cap.

### Neutral

- Redis stays the warm datasource cache; package caches stay cold per run,
  exactly as under option A.
- INV-0001's success criteria do not change.
- The "no RBAC" positive of ADR-0003 is given up on purpose.

## Alternatives Considered

- **Option A with a runner sidecar** (DESIGN-0001's previous revision):
  more code for a weaker boundary, and the pool still needs a scaler.
- **Option A, single container, on a trusted-fleet assumption:** rejected by
  the ecosystem table above; Python lockfile updates run third-party code.
- **KEDA `ScaledJob` creating the Jobs** (ADR-0008's "try first" row): takes
  RBAC out of the worker, but KEDA creates Jobs from one template, so the
  per-run profile and token Secret would have to be fetched by the Job
  itself, which needs a credential inside the pod again. The
  activity-created Job keeps the per-run spec. Revisit if API churn ever
  matters.
- **A sandboxing runtime class for every run:** a profile setting, not a
  shape; the Python profile may use it.

## References

- [INV-0001](../investigation/0001-temporal-as-the-renovate-control-plane.md)
  § Execution model, options A and B
- [ADR-0003](0003-worker-pool-execs-the-renovate-cli.md) (superseded),
  [ADR-0008](0008-scale-runs-within-the-installation-budget.md) (amended)
- [DESIGN-0001](../design/0001-boopd-spike-workflows-runrenovate-activity-and-worker.md)
- renovate-operator `0183661`: `internal/jobspec/job_builder.go`,
  `internal/jobspec/env.go`
- Renovate self-hosted options: `customEnvVariables`, `ignoreScripts`,
  `allowScripts`, `reportType`
- Kubernetes: [Jobs](https://kubernetes.io/docs/concepts/workloads/controllers/job/)
  (`suspend`, `activeDeadlineSeconds`, `ttlSecondsAfterFinished`),
  [Pod Security Standards](https://kubernetes.io/docs/concepts/security/pod-security-standards/)
- pip `PIP_ONLY_BINARY`, uv `UV_NO_BUILD`
