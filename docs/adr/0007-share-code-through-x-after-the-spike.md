---
id: ADR-0007
title: "Share code through donaldgifford/x after the spike"
status: Proposed
author: Donald Gifford
created: 2026-10-01
---

<!-- markdownlint-disable-file MD025 MD041 -->

# ADR-0007: Share code through donaldgifford/x after the spike

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

The spike copies code from renovate-operator and repo-guardian. Phase 0 of the
real build moves the shared pieces into `donaldgifford/x`, one Go module
versioned together, and `boopd` and repo-guardian import them from there.

## Context

`boopd` needs code that exists elsewhere (INV-0001 Observations 7 and 8):

| Piece | Source | LOC |
| ----- | ------ | --- |
| Platform clients: discovery, config probe, token minting, error classification | renovate-operator `internal/platform` | ~1,060 |
| Temporal config, mTLS/OIDC, dial, worker options, versioning, schedules, backlog | repo-guardian `v2` `internal/temporal` | ~820 |
| Installation budget entity, priority and fairness presets | repo-guardian `v2` `internal/workflows` | ~410 |

Extracting first would hold the spike up on API design for a library whose
needs are not yet known.

## Decision

- **Spike:** copy and own the copies. `internal/platform` is already copied
  from renovate-operator `0183661`. The Temporal pieces come from
  repo-guardian `feat/impl-0028-controls-foundations` @ `d1f20a0`.
- **Phase 0, after the spike:** extract these into `donaldgifford/x`, one
  module with all packages versioned together. Consumers pin one `x` version.
  `boopd` and repo-guardian switch to it. renovate-operator may switch, but
  does not have to.
- **Record where each copy came from in its package doc**, so the extraction
  can diff against the source.

## Consequences

### Positive

- The spike is not blocked on library design.
- Phase 0 extracts code shaped by two real consumers.

### Negative

- Copies drift until Phase 0. Fixes made in one copy have to be applied by
  hand to the others meanwhile.

### Neutral

- Lint and style fixes made to the copies (for example `%w: %w` wrapping)
  carry into `x`.

## Alternatives Considered

- **Extract to `x` before the spike:** delays the spike and fixes an API
  before it is proven.
- **Copy permanently:** drift becomes permanent.
- **Import renovate-operator or repo-guardian directly:** their code is under
  `internal/`, and importing it would tie releases together.

## References

- INV-0001 Observations 7, 8, OQ6
