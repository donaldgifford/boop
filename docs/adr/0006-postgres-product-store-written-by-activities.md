---
id: ADR-0006
title: "Postgres product store written by activities"
status: Proposed
author: Donald Gifford
created: 2026-10-01
---

<!-- markdownlint-disable-file MD025 MD041 -->

# ADR-0006: Postgres product store written by activities

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

`boopd` keeps a Postgres store of repositories, runs and proposed updates,
behind its own HTTP API and UI. Activities write it, and the API reads it.
Nothing in it drives scheduling. It is built after the spike.

## Context

Temporal history is the wrong place to query "which PRs are open across the
fleet and how risky are they". The planned later features need durable,
queryable product data (INV-0001 § Beyond v1):

- **Automerge confidence from evals:** each Renovate PR is a record that eval
  results attach to.
- **A security-findings priority report:** joins findings to pending bumps on
  package, affected range and target version.

renovate-operator's planned Postgres was scheduling state. This one is
different (INV-0001 Observation 2, OQ5).

## Decision

- **Postgres** (CNPG in the homelab, following repo-guardian's layout) is the
  record of repositories, runs and proposed updates.
- **Temporal owns execution state. The store owns results.** Workflows write to
  the store only through activities, and the API reads the store. The API
  talks to Temporal only to signal (`recheck`) or describe workflows.
- **v1 must record**, without needing later migrations:
  - one record per proposed update: repository, branch, PR number, and per
    dependency the manager, package, from-version and to-version;
  - a stable PR identity across rebases.
- **Spike scope:** no store. The spike carries the same data in activity
  results and logs. The schema is defined in the v1 DESIGN.

## Consequences

### Positive

- It is a product, not just a scheduler. History, per-repository state and
  later views all have a home.
- Evals and findings attach to stable records.

### Negative

- A second source of state next to Temporal. The two can drift, so every kind
  of data needs a single owner.
- Another stateful dependency to run.

### Neutral

- Store writes are activities with unlimited retry (repo-guardian's
  `storeOptions` preset), so a store outage pauses recording but loses
  nothing.

## Alternatives Considered

- **No database: Temporal history, logs and metrics only** (OQ5 a): cannot
  serve fleet-wide queries or the planned later features.
- **A small results table for a status page** (OQ5 b): too narrow for the
  planned later features and would need migrating.

## References

- INV-0001 Observation 2, OQ5, § Beyond v1
- RFC-0001
