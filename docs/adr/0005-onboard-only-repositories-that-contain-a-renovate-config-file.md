---
id: ADR-0005
title: "Onboard only repositories that contain a Renovate config file"
status: Proposed
author: Donald Gifford
created: 2026-10-01
---

<!-- markdownlint-disable-file MD025 MD041 -->

# ADR-0005: Onboard only repositories that contain a Renovate config file

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

A repository takes part in `boopd` if, and only if, its default branch contains
a Renovate config file. repo-guardian writes that file. Renovate's own
onboarding is turned off.

## Context

repo-guardian already enforces managed files across repositories and is the
natural owner of "this repository should use Renovate". Letting Renovate open
its own onboarding PRs would give two systems the same job. A discovery filter
in `boopd`'s config would move the decision away from the repository
(INV-0001 Observation 9).

## Decision

- **Discovery** lists every repository the installation can see. For GitHub
  Apps that means `/installation/repositories`, per renovate-operator
  INV-0004. It keeps only those where `HasRenovateConfig` finds one of
  `platform.ConfigPaths` on the default branch, and only those get a
  `RepoWorkflow`.
- **Renovate runs with `onboarding: false` and `requireConfig: required`.**
  This is a second guard if discovery is stale.
- **Removing the file takes the repository out of discovery.** The next
  discovery pass signals its `RepoWorkflow` to end.
- **Pushes that add the file** are the first webhook worth handling once
  ingest exists. Until then the next discovery pass picks them up.

## Consequences

### Positive

- One owner for onboarding. Repository owners opt in or out by committing a
  file.
- No onboarding PRs from `boop-bot`.
- Discovery cost is bounded: one config probe per visible repository per
  pass.

### Negative

- Onboarding latency is up to one discovery interval until webhook ingest
  exists.
- `boopd` relies on repo-guardian to roll Renovate out. Without it, someone
  has to add the file by hand.

### Neutral

- Archived repositories and forks can still be filtered in discovery.

## Alternatives Considered

- **Renovate's own onboarding PRs:** a second onboarding owner, and PR noise
  from `boop-bot`.
- **An allowlist or glob filter in `boopd` config:** central, but disconnected
  from the repository. It can still be layered on as an exclusion list.

## References

- INV-0001 Observation 9
- renovate-operator INV-0004 (App-grant-aware discovery)
