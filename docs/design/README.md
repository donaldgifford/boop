# Design Documents

This directory contains detailed design documents for feature implementation.

## What are Design Documents?

Design documents describe **how a feature or system will be built**. Each design
document includes:

- **Overview**: What is being designed and why
- **Goals and Non-Goals**: Scope boundaries
- **Detailed Design**: Architecture, APIs, data models
- **Testing Strategy**: How the design will be validated
- **Migration Plan**: How to roll out the changes

## Creating a New Design Document

```bash
docz create design "Your Design Title"
```

## Design Status

- **Draft**: Initial draft, still being written
- **In Review**: Ready for review and feedback
- **Approved**: Approved and ready for implementation
- **Implemented**: Design has been fully implemented
- **Abandoned**: Design was not pursued

<!-- BEGIN DOCZ AUTO-GENERATED -->
## All Design

<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
| ID | Title | Status | Date | Author | Link |
| --- | --- | --- | --- | --- | --- |
| DESIGN-0001 | boopd spike: workflows, RunRenovate activity and worker | Approved | 2026-10-01 | Donald Gifford | [0001-boopd-spike-workflows-runrenovate-activity-and-worker.md](0001-boopd-spike-workflows-runrenovate-activity-and-worker.md) |
| DESIGN-0002 | Valkey as the bundled Renovate cache | Draft | 2026-10-15 | Donald Gifford | [0002-valkey-as-the-bundled-renovate-cache.md](0002-valkey-as-the-bundled-renovate-cache.md) |
| DESIGN-0003 | Webhook ingest: GitHub events to RepoWorkflow signals | Implemented | 2026-10-17 | Donald Gifford | [0003-webhook-ingest-github-events-to-repoworkflow-signals.md](0003-webhook-ingest-github-events-to-repoworkflow-signals.md) |
| DESIGN-0004 | boopd API and management UI: event store, PR inventory and estate view | Draft | 2026-10-18 | Donald Gifford | [0004-boopd-api-and-management-ui-event-store-pr-inventory-and-estate.md](0004-boopd-api-and-management-ui-event-store-pr-inventory-and-estate.md) |
| DESIGN-0005 | Sandboxed execution of repository code in Renovate runs | Draft | 2026-10-24 | Donald Gifford | [0005-sandboxed-execution-of-repository-code-in-renovate-runs.md](0005-sandboxed-execution-of-repository-code-in-renovate-runs.md) |
<!-- markdownlint-restore -->
<!-- END DOCZ AUTO-GENERATED -->
