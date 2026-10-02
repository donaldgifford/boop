---
id: ADR-0004
title: "No CRDs; configuration from a chart-shipped file"
status: Proposed
author: Donald Gifford
created: 2026-10-01
---

<!-- markdownlint-disable-file MD025 MD041 -->

# ADR-0004: No CRDs; configuration from a chart-shipped file

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

`boopd` has no CRDs. Platform, discovery-schedule and Renovate defaults live in
one config file rendered from Helm values and mounted into every pod. It is
reconciled into Temporal Schedules at worker start.

## Context

renovate-operator exposes `RenovatePlatform`, `RenovateScan` and `RenovateRun`
CRDs. renovate-operator RFC-0001 justified them on RBAC split, `kubectl`
ergonomics and GitOps. In `boopd` each of those has another answer (INV-0001
Observation 6):

- **RBAC split:** app teams control Renovate through the config file in
  their repository and the shared preset. Whether a repository takes part
  depends on that file (ADR-0005).
- **Visibility:** `boopd`'s own API and UI (ADR-0006), with the Temporal UI
  for operators.
- **GitOps:** the config file ships in the chart values.

## Decision

- No CRDs and no controller-runtime.
- **One YAML config file** (platforms, discovery schedule, per-repository
  cadence, Renovate global config), rendered from chart values into a
  ConfigMap and mounted read-only.
- **Secrets come from Kubernetes Secrets**, referenced by path or env and never
  inlined in the file: App private key and Redis password.
- **At worker start, `boopd` reconciles the file into Temporal**: it creates
  or updates the discovery Schedules (`EnsureSchedule`) and deletes Schedules
  for platforms that are no longer configured.
- Changing config means a chart upgrade, which rolls the pods.

## Consequences

### Positive

- One control plane. A CRD front end would add a second one, which is what
  repo-guardian's one-mechanism rule (repo-guardian DESIGN-0021) exists to
  prevent.
- No CRD versioning, conversion webhooks or CEL validation to maintain.
- The worker needs no Kubernetes API access.

### Negative

- No `kubectl get` view of runs. Visibility depends on the API and UI, which
  come after the spike. Until then it is the Temporal UI and logs.
- Config changes need a rollout, not a live edit.

### Neutral

- Users who want CRDs still have renovate-operator.

## Alternatives Considered

- **Keep thin Platform and Scan CRDs that render into Schedules**
  (INV-0001 OQ3 b): two control planes for little gain.
- **Store config in Postgres and edit it through the API:** possible later.
  It is not GitOps-shaped and would need the store before the spike.

## References

- INV-0001 Observation 6, OQ3
- repo-guardian DESIGN-0021 (one mechanism), `PolicyRolloutWorkflow`
