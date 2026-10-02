---
id: ADR-0008
title: "Scale runs within the installation budget; choose the scaling mechanism after the spike"
status: Proposed
author: Donald Gifford
created: 2026-10-01
---

<!-- markdownlint-disable-file MD025 MD041 -->

# ADR-0008: Scale runs within the installation budget; choose the scaling mechanism after the spike

<!--toc:start-->
- [Summary](#summary)
- [Context](#context)
- [Decision](#decision)
  - [In the spike](#in-the-spike)
  - [Fast follow after the spike](#fast-follow-after-the-spike)
- [Consequences](#consequences)
  - [Positive](#positive)
  - [Negative](#negative)
  - [Neutral](#neutral)
- [Alternatives Considered](#alternatives-considered)
- [References](#references)
<!--toc:end-->

## Summary

`boopd` scales on three layers:

- **Workflows** follow the number of repositories.
- **Concurrent Renovate runs** are capped by each GitHub App installation's
  rate budget, which is discovered at run time, not configured.
- **Renovate workers** follow the runs the budget has admitted.

The budget rules are part of the spike. The mechanism that scales workers
(KEDA trigger, Deployment or Job per run) is chosen in a fast follow after the
spike. Until then workers run at a fixed replica count.

## Context

Three things grow with the fleet, at very different cost:

| Layer | Count | Cost |
| ----- | ----- | ---- |
| `RepoWorkflow` | one per onboarded repository (50 repos → 50 workflows, 5,000 → 5,000) | almost nothing: a sleeping workflow is a timer in Temporal's persistence |
| Renovate runs in flight | as many as are due and admitted | one Renovate process each, plus GitHub API spend |
| Renovate workers | one per run in flight (one activity slot per pod, ADR-0003) | a pod with the Renovate full image |

The binding constraint is the GitHub App installation's rate budget, not
compute.

**The budget differs by plan and changes over time.** Per GitHub's docs:

- github.com installations start at 5,000 REST requests per hour and grow with
  repositories and users, up to 12,500;
- GitHub Enterprise Cloud installations get 15,000;
- on GitHub Enterprise Server the admin sets it, and may disable it.

**The budget is not one number:**

- Renovate's GitHub lookups use GraphQL heavily, and GraphQL has its own
  limit;
- secondary limits cap concurrent requests and per-minute spend;
- one App installed on several organisations has a separate budget per
  installation.

**Admission comes before scheduling.** `RepoWorkflow` acquires a lease from
`InstallationWorkflow` before it schedules `RunRenovate` on the separate
`boopd-renovate` task queue (DESIGN-0001). So that queue's backlog only ever
holds work the budget has already admitted, and anything that scales workers on
it stays inside the budget.

**Two ways to drive scaling.** repo-guardian v2 scales workers with KEDA's
`temporal` trigger. That trigger cannot authenticate with OIDC, so
repo-guardian is moving its homelab workers to mTLS to use it. `boopd` could
use the same trigger, or publish its own metric and let KEDA read that instead.

## Decision

### In the spike

- **Workflows scale with repositories.** One `RepoWorkflow` per onboarded
  repository (ADR-0002), with no cap.
- **Discover the budget.** `InstallationWorkflow` reads `GET /rate_limit` with
  the installation token and takes `limit`, `remaining` and `reset` from the
  response. It does so on first start, on every run report (`RunRenovate`
  reads it before and after each run) and at least hourly. It tracks the
  `core` (REST) and `graphql` resources separately. Nothing in config states
  the limit or the plan.
- **Admit against the tightest resource.** A lease is granted only if every
  tracked resource can cover the estimated spend and still keep its reserve.
  The reserve is a fraction of that resource's discovered `limit`. Spend per
  run is estimated per resource with an EWMA from the before/after readings.
- **Concurrent runs per installation =** the leases the budget admits,
  optionally capped by `maxConcurrentRuns` in config. The cap is a safety
  limit for secondary rate limits and cluster capacity, not a budget figure.
- **Every installation of the App has its own `InstallationWorkflow`.**
  The fleet-wide ceiling is the sum across installations.
- **Workers run at a fixed replica count.**
- **Emit the signals a scaler would use**, so the fast follow can compare
  them on real data:
  - `boopd-renovate` queue backlog;
  - `boopd_budget_admitted_runs{installation}`;
  - `boopd_renovate_desired_workers`, computed by `boopd` from admitted runs
    and runs in flight.
- **Keep the doors open:**
  - `RunRenovate` runs on its own task queue;
  - the activity holds no state on the worker between runs;
  - the worker stops polling on `SIGTERM`.

### Fast follow after the spike

An investigation picks the scaling mechanism on the spike's measured numbers:

| Shape | Signal | Notes |
| ----- | ------ | ----- |
| Worker Deployment + KEDA `ScaledObject`, `temporal` trigger | `boopd-renovate` backlog | needs mTLS or an API key for the trigger; scale-down kills runs in flight unless the termination grace period covers a run |
| Worker Deployment + KEDA `ScaledObject`, `prometheus` or `metrics-api` trigger | `boopd_renovate_desired_workers` | KEDA never talks to Temporal, so no Temporal auth problem; the same pattern could serve repo-guardian |
| One Job per run via KEDA `ScaledJob` (try first) | either signal | each repository's run gets its own pod, like renovate-operator's isolation, without the worker holding RBAC; costs pod start and image pull per run |
| Activity creates a Job per run (ADR-0003 option B) | n/a | worker needs RBAC for Jobs and Secrets |

The investigation also settles `minReplicas`/`maxReplicas`, scale-to-zero and
cold start, and what a node drain does to a run in flight. If it picks a Job
per run, ADR-0003 is superseded.

## Consequences

### Positive

- 50 or 5,000 repositories is a change in workflow count only. Compute
  follows admitted work.
- No plan-specific configuration. github.com, Enterprise Cloud and a GHES
  limit are all read from the API, and the budget follows an installation
  whose limit grows as repositories are added.
- Because admission comes before scheduling, any scaler on the Renovate
  queue is budget-bounded by construction.
- The spike's numbers (run duration, spend per resource, pod start) decide
  the scaling mechanism, not guesses.

### Negative

- Budget accounting is admission, not metering. Renovate spends inside Node,
  and Go only sees before/after readings. Overlapping runs make per-run spend
  noisy (INV-0001 Observation 5).
- Secondary rate limits are not visible in `/rate_limit`. Renovate backs off
  on its own, and `maxConcurrentRuns` is the only guard.
- Fixed replicas until the fast follow: sized by hand, idle between waves.

### Neutral

- `boop-bot` must be used by `boopd` alone, or other spend shows up in the
  readings as noise (INV-0001 Observation 5).

## Alternatives Considered

- **Configure the budget per installation:** wrong as soon as the plan or the
  repository count changes. `/rate_limit` reports the real figure.
- **Track REST only:** misses GraphQL, which Renovate's GitHub datasources
  lean on.
- **Gate in the worker instead of the workflow** (take a task, then wait for
  budget): workers sit holding tasks, and backlog stops meaning admitted work.
- **Pick the KEDA mechanism now:** commits to a shape before knowing run
  durations, cold start and scale-down behaviour.
- **HPA on CPU or memory:** Renovate's CPU use does not track the work
  waiting.

## References

- ADR-0001, ADR-0002, ADR-0003
- DESIGN-0001 § InstallationWorkflow, § RunRenovate activity
- INV-0001 Observation 5
- repo-guardian `v2` @ `278c7ec`: `internal/workflows/installation.go`,
  `charts/repo-guardian/templates/worker-scaledobject.yaml`
- [GitHub REST: rate limits for GitHub Apps](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api)
- [GitHub REST: rate limit endpoint](https://docs.github.com/en/rest/rate-limit/rate-limit)
- [KEDA Temporal scaler](https://keda.sh/docs/latest/scalers/temporal/)
- [KEDA ScaledJob](https://keda.sh/docs/latest/reference/scaledjob-spec/)
