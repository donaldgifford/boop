# Architecture Decision Records (ADRs)

This directory contains Architecture Decision Records documenting significant
technical decisions.

## What are ADRs?

ADRs document **technical implementation decisions** for specific architectural
components. Each ADR focuses on a single decision and includes:

- **Context**: The problem or constraint that led to this decision
- **Decision**: What was chosen and why
- **Consequences**: Trade-offs, pros, and cons
- **Alternatives**: Other options that were considered

## Creating a New ADR

```bash
docz create adr "Your ADR Title"
```

## ADR Status

- **Proposed**: Under discussion, not yet approved
- **Accepted**: Approved and being implemented or already implemented
- **Deprecated**: No longer relevant or superseded
- **Superseded by ADR-XXXX**: Replaced by another ADR

<!-- BEGIN DOCZ AUTO-GENERATED -->
## All ADRs

| ID | Title | Status | Date | Author | Link |
|----|-------|--------|------|--------|------|
| ADR-0001 | Use Temporal as the control plane | Proposed | 2026-10-01 | Donald Gifford | [0001-use-temporal-as-the-control-plane.md](0001-use-temporal-as-the-control-plane.md) |
| ADR-0002 | One entity workflow per repository | Proposed | 2026-10-01 | Donald Gifford | [0002-one-entity-workflow-per-repository.md](0002-one-entity-workflow-per-repository.md) |
| ADR-0003 | Worker pool execs the Renovate CLI | Superseded | 2026-10-01 | Donald Gifford | [0003-worker-pool-execs-the-renovate-cli.md](0003-worker-pool-execs-the-renovate-cli.md) |
| ADR-0004 | No CRDs; configuration from a chart-shipped file | Proposed | 2026-10-01 | Donald Gifford | [0004-no-crds-configuration-from-a-chart-shipped-file.md](0004-no-crds-configuration-from-a-chart-shipped-file.md) |
| ADR-0005 | Onboard only repositories that contain a Renovate config file | Proposed | 2026-10-01 | Donald Gifford | [0005-onboard-only-repositories-that-contain-a-renovate-config-file.md](0005-onboard-only-repositories-that-contain-a-renovate-config-file.md) |
| ADR-0006 | Postgres product store written by activities | Proposed | 2026-10-01 | Donald Gifford | [0006-postgres-product-store-written-by-activities.md](0006-postgres-product-store-written-by-activities.md) |
| ADR-0007 | Share code through donaldgifford/x after the spike | Proposed | 2026-10-01 | Donald Gifford | [0007-share-code-through-x-after-the-spike.md](0007-share-code-through-x-after-the-spike.md) |
| ADR-0008 | Scale runs within the installation budget; choose the scaling mechanism after the spike | Proposed | 2026-10-01 | Donald Gifford | [0008-scale-runs-within-the-installation-budget.md](0008-scale-runs-within-the-installation-budget.md) |
| ADR-0009 | Run each Renovate run as a Kubernetes Job | Proposed | 2026-10-07 | Donald Gifford | [0009-run-each-renovate-run-as-a-kubernetes-job.md](0009-run-each-renovate-run-as-a-kubernetes-job.md) |
<!-- END DOCZ AUTO-GENERATED -->
