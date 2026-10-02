---
id: ADR-0002
title: "One entity workflow per repository"
status: Proposed
author: Donald Gifford
created: 2026-10-01
---

<!-- markdownlint-disable-file MD025 MD041 -->

# ADR-0002: One entity workflow per repository

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

The unit of work is one repository. Each onboarded repository has a long-lived
`RepoWorkflow`. Each installation has an `InstallationWorkflow` budget entity.
Discovery is a short `DiscoveryWorkflow` started by a Temporal Schedule.

## Context

renovate-operator batches thousands of repositories into a Run because it
launches one Job per Run. That batching causes the token-lifetime, ConfigMap
and all-or-nothing limits. Webhook triggers and per-repository deduplication
need a stable identity per repository (INV-0001 Observation 3, OQ4).

## Decision

| Workflow | ID | Started by | Lifetime |
| -------- | -- | ---------- | -------- |
| `RepoWorkflow` | `repo/<platform>/<repo id>` | discovery (`SignalWithStart`), later webhooks | until the repository leaves discovery; ContinueAsNew periodically |
| `InstallationWorkflow` | `installation/<platform>/<id>` | first `acquire` (Update-with-Start) | long-lived |
| `DiscoveryWorkflow` | `discovery/<platform>/<schedule time>` | Temporal Schedule | one run |

`<repo id>` is the platform's numeric repository ID, not the `owner/name`
slug, so renames and transfers keep the same workflow.

`RepoWorkflow` loops: wait for the next due time or a `recheck` signal, acquire
budget, run `RunRenovate`, report spend, and record the outcome. The
repository's cadence is a jittered timer, which spreads load across the window
without a separate scheduler.

## Consequences

### Positive

- One bad repository affects only itself.
- Webhooks, manual rechecks and discovery all converge on one ID, which gives
  deduplication for free.
- Per-repository state (last run, stall count, last report) lives in a single
  place.

### Negative

- Tens of thousands of long-lived workflows. Needs ContinueAsNew discipline
  and versioning for workflow code changes.
- Repositories that leave discovery need an explicit end signal, or their
  workflows linger.

### Neutral

- The set matches repo-guardian v2's workflows minus policy rollout,
  bootstrap and snapshots.

## Alternatives Considered

- **One short workflow per pass that fans out activities** (INV-0001 OQ4 b):
  simpler, but has no stable per-repository identity for webhooks or
  deduplication, and per-repository state has to live somewhere else.
- **Child workflow per repository under a pass workflow:** same identity
  problem across passes.

## References

- INV-0001 Observation 3, OQ4
- repo-guardian DESIGN-0026 § Workflows
