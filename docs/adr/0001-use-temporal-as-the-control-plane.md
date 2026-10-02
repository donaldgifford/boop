---
id: ADR-0001
title: "Use Temporal as the control plane"
status: Proposed
author: Donald Gifford
created: 2026-10-01
---

<!-- markdownlint-disable-file MD025 MD041 -->

# ADR-0001: Use Temporal as the control plane

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

`boopd` uses Temporal for scheduling, retries, concurrency, rate-budget
admission and run history. It has no hand-built queue, scheduler or leader
election.

## Context

Running Renovate across a fleet needs a scheduler with:

- admission control against a shared rate budget;
- time-spreading;
- per-repository retry and failure isolation;
- event triggers;
- a way to resume after a crash.

renovate-operator DESIGN-0001 planned to build that scheduler on an
operator-owned Postgres. repo-guardian v1 built the same thing on Valkey and
replaced it with Temporal in v2 (repo-guardian INV-0019, DESIGN-0026), which is
now running at `v2.0.0-rc.4`. Its Temporal plumbing, budget entity and
deployment reference can be copied (INV-0001 Observation 8).

## Decision

Temporal is the only control plane:

| Concern | Temporal primitive |
| ------- | ------------------ |
| Periodic discovery | Schedule |
| Per-repository cadence and time-spreading | jittered workflow timer |
| At most one run per repository | workflow ID uniqueness |
| Retry, backoff, wait-until-reset | activity retry policy, `NextRetryDelay` |
| Shared rate budget | `InstallationWorkflow` (Update + Signal entity) |
| Event triggers | `SignalWithStart` |
| Worker scaling | KEDA on admitted work; trigger and shape chosen after the spike (ADR-0008) |
| Fairness across installations | task-queue fairness key = installation |

Temporal Server 1.31 or later is required, for fairness GA,
Update-with-Start GA and `NextRetryDelay` (repo-guardian DESIGN-0026).
`boopd` runs in its own Temporal namespace, `boopd`.

## Consequences

### Positive

- None of the planned scheduler (shard plans, budget ledger, retry
  bookkeeping, leader election) has to be written.
- Every v0.1 limit has a direct primitive (INV-0001 Observation 2).
- About 1,200 lines of tested plumbing can be copied from repo-guardian v2.

### Negative

- A Temporal cluster and its persistence are new things to run.
- A new class of failure: workflow determinism and versioning.
- Workflow state is opaque to `kubectl`. Visibility comes from the Temporal UI
  and, later, `boopd`'s own API.

### Neutral

- The data store keeps a product role (ADR-0006), but it never holds
  scheduling state.

## Alternatives Considered

- **Hand-built scheduler on Postgres** (renovate-operator DESIGN-0001 plan):
  rebuilds what repo-guardian v1 built and then deleted.
- **Kubernetes controllers and Jobs** (renovate-operator v0.1): no shared
  budget or per-repository retry, and Job-shaped units of work.
- **A queue (Valkey/Redis, NATS) plus workers:** repo-guardian v1's path. It
  needs delayed requeue, reapers and leases built by hand.

## References

- INV-0001 Observations 1, 2, 8
- repo-guardian INV-0019, DESIGN-0026
- RFC-0001
