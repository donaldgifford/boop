---
id: ADR-0003
title: "Worker pool execs the Renovate CLI"
status: Proposed
author: Donald Gifford
created: 2026-10-01
---

<!-- markdownlint-disable-file MD025 MD041 -->

# ADR-0003: Worker pool execs the Renovate CLI

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

The `RunRenovate` activity execs the Renovate CLI in the worker's own pod, one
repository per activity and one activity slot per pod. The worker image is
the Renovate full image plus the Go worker binary. If the spike's isolation
criteria fail, the fallback is a Kubernetes Job per repository.

## Context

Renovate is a Node CLI, so unlike repo-guardian's Go engine it cannot run
in-process. INV-0001 Observation 4 compared three execution options:

| | A: worker execs Renovate | B: Job per repository | C: today's Indexed Job |
| - | - | - | - |
| Worker RBAC | none | Jobs, Secrets | as today |
| Token lifetime | solved | solved | not solved |
| Per-repository retry | yes | yes | no |
| Scaling | KEDA on backlog | Job churn (~30k pods/day at fleet scale) | fixed |
| Isolation | fresh directories, shared pod | pod per repository | pod per shard |

## Decision

Option A, with these isolation rules:

- **Toolchains are baked into the image.** `binarySource=global` and a
  read-only root filesystem, so nothing installs toolchains at run time.
- **The datasource cache is shared through Redis** (`redisUrl`). Only the
  Renovate process writes to it.
- **Each activity gets fresh `baseDir` and `cacheDir`** on an `emptyDir`,
  deleted when the activity ends, along with anything under `/tmp`.
- **One activity slot per pod.** No concurrent neighbours.
- **The token is minted inside the activity**, passed by environment and
  never written to disk.
- **Renovate's script and env controls stay at their defaults:**
  `allowScripts=false`, `allowedCommands=[]`, `exposeAllEnv=false`.

The spike has to show that a package-manager step cannot leave readable state
for the next activity, and cannot see the token, `RENOVATE_*` variables or the
Redis credentials (INV-0001 § Spike, isolation criteria). If it can, switch to
option B.

## Consequences

### Positive

- The worker needs no Kubernetes RBAC.
- Can scale on Temporal backlog with KEDA. The spike uses fixed replicas,
  and the scaling design comes after it (ADR-0008).
- No pod start-up per repository.
- Datasource lookups stay warm through Redis.

### Negative

- Isolation between repositories is per-process and per-directory, not
  per-pod. A child process that escapes far enough to read the parent's memory
  or environment has beaten that isolation, and only option B defends against
  it.
- Package-manager caches start cold for every repository.
- Big image: the Renovate full image plus every baked toolchain.

### Neutral

- Cache warmth no longer separates A from B, since both use Redis and fresh
  disk. What separates them is pod churn, RBAC and start-up latency.

## Alternatives Considered

- **B: Job per repository:** stronger isolation, but pod churn, cluster write
  access for the worker and per-repository start-up cost. Kept as the
  fallback.
- **C: Temporal launching today's Indexed Job:** keeps the limits that
  motivated `boopd`.

## References

- INV-0001 Observation 4, OQ2, § Spike
- ADR-0008: the scaling fast follow tries a Job per run first and may
  supersede this decision
- [Renovate self-hosted configuration](https://docs.renovatebot.com/self-hosted-configuration/)
