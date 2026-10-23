# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
this project adheres to [Semantic Versioning](https://semver.org/).
## [unreleased]

### Documentation

- Refresh ADR, RFC and IMPL statuses
- *(impl)* IMPL-0005 findings on the real fixtures and the copy pin

### Testing

- *(renovate)* Real Renovate 44 fixtures (IMPL-0001 4.5)

### Miscellaneous Tasks

- *(scripts)* Phase8-fixtures.sh creates the IMPL-0001 8.1 fixtures

## [0.5.2] - 2026-10-22

### Bug Fixes

- *(chart)* Count the ingest replicas in the runs quota

### Documentation

- *(chart)* Ingest needs egress to the OIDC token endpoint
- *(impl)* IMPL-0004 homelab setup and timings

## [0.5.1] - 2026-10-21

### Documentation

- *(design)* DESIGN-0003 implemented in v0.4.0 and v0.5.0

## [0.5.0] - 2026-10-19

### Features

- *(ingest)* Verify a delivery's sha256 signature for its App
- *(ingest)* Decode deliveries into go-github's typed events
- *(ingest)* Route deliveries by DESIGN-0003's table
- *(ingest)* Delivery records and the log sink
- *(ingest)* The webhook handler runs routed actions against Temporal
- *(observability)* Ingest delivery and Temporal-call metrics
- *(cmd)* Boopd ingest serves deliveries with health and metrics
- *(chart)* Ingest values and the ingest and webhook blocks in boopd.hcl
- *(chart)* The ingest Deployment, ServiceAccount, Service and PDB
- *(chart)* Ingress and HTTPRoute routing only the ingest path
- *(chart)* Ingest NetworkPolicy with DNS and Temporal egress only
- *(chart)* Ingest delivery alerts and ServiceMonitor

### Bug Fixes

- *(activities)* CheckRepo re-mints after a revoked installation token
- *(chart)* Mount extraVolumes in the ingest pods

### Documentation

- Webhooks guide, config chart notes and the ingest layout
- *(impl)* IMPL-0004 Phase 11 notes
- *(impl)* IMPL-0004 PRs opened; merge and homelab tasks deferred

### Testing

- *(ingest)* Routing table, signature and handler tests
- *(cmd)* Boopd ingest usage, config errors and a dev-server start and stop
- *(chart)* Ingest suite for credentials, routes, policy, guards and schema
- *(e2e)* Webhook ingest scenarios against the chart in k3d

### Miscellaneous Tasks

- *(chart)* Validate the ingest render and regenerate the chart README

## [0.4.0] - 2026-10-19

### Features

- *(config)* Ingest block and per-App webhook block
- *(config)* Read webhook secrets for ingest, apart from the App key
- *(activities)* PlanRun returns the ingest settle and min_interval
- *(github)* CheckRepo returns the config probe; a mint 404 is ErrInstallationGone
- *(activities)* CheckRepo offboards deleted installations and refreshes extends
- *(workflows)* Event and changed signal types and RepoState's pending event
- *(workflows)* RepoWorkflow handles event and changed signals
- *(workflows)* Targeted discovery of named installations
- *(workflows)* TargetedDiscoveryID
- *(github)* List the App's hook deliveries with the App JWT
- *(activities)* ListHookDeliveries audits the App's webhook deliveries
- *(workflows)* DeliveryAuditWorkflow counts failed deliveries per pass
- *(worker)* A delivery-audit Schedule per App with a webhook

### Bug Fixes

- *(activities)* CheckRepo re-mints after a revoked installation token

### Documentation

- Approve DESIGN-0003 and note it in DESIGN-0001 and INV-0001
- *(design-0003)* Fix a line read as a heading
- *(examples)* Ingest block and boop-bot's webhook block
- *(config)* Ingest and webhook settings

### Testing

- *(workflows)* Record replay baselines from the current code
- *(workflows)* Replay the recorded histories in just test
- *(fakegithub)* Signed deliveries, hook deliveries and deleted installations
- *(fakegithub)* Payload builders for the routed webhook events
- *(fakegithub)* Cover deliveries, hook-delivery paging, deleted installations and payloads
- *(workflows)* Event and changed timings on the test clock
- *(workflows)* Targeted discovery passes and the untargeted replay

## [0.3.2] - 2026-10-19

### Features

- *(just)* Temporal recipe through temporal-oidc

### Bug Fixes

- *(github)* Keep a 429's Retry-After in RateLimitedError
- *(discovery)* Retry after GitHub's Retry-After, not the fixed backoff

### Documentation

- Close IMPL-0003 (v0.3.1 shipped, fartlab discovery on GraphQL)
- *(design)* DESIGN-0003 webhook ingest
- *(design)* DESIGN-0004 event store, API and management UI
- *(impl)* IMPL-0004 webhook ingest
- *(impl)* IMPL-0005 event store, API and management UI
- Note the #16 fix in INV-0002 and DESIGN-0003

### Testing

- *(fakegithub)* Secondary rate limits and probe arrival times
- *(activities)* Discovery honours a secondary limit's Retry-After
- *(workflows)* DiscoveryWorkflow retries after NextRetryDelay
- *(e2e)* Discovery retries after a secondary limit's Retry-After

## [0.3.1] - 2026-10-16

### Features

- *(activities)* Heartbeat per probe query; count probe splits and fallbacks

### Bug Fixes

- *(platform)* Size GraphQL probe batches from a lookup budget
- *(platform)* Split timed-out GraphQL probes and fall back to REST
- *(platform)* RetryAfter on a primary rate limit is the time until reset

### Documentation

- Verify release provenance against main; close IMPL-0002
- *(design)* DESIGN-0002 valkey as the bundled Renovate cache
- *(design)* DESIGN-0002 apply OQ answers
- *(design)* DESIGN-0002 resolve OQ6 and OQ11, expand OQ10
- *(design)* DESIGN-0002 resolve OQ10
- *(investigation)* INV-0002 GraphQL config probe times out on large batches
- INV-0002 decisions, interim probe note, IMPL-0003 plan for issue 14
- Restore IMPL-0002 toc anchors
- *(impl)* IMPL-0003 record OQ decisions
- GraphQL probe budget, split and fallback; amend DESIGN-0001
- *(impl)* IMPL-0003 in progress
- *(impl)* IMPL-0003 PR open and green; post-merge steps deferred

### Testing

- *(fakegithub)* GitHub's GraphQL limits: lookup timeout, timeout shapes, 100-id cap
- *(e2e)* Discovery falls back to REST when every GraphQL probe times out

## [0.3.0] - 2026-10-13

### Features

- *(chart)* Redis.auth.existingSecret for GitOps installs
- *(config)* Redis_url with a password-only Secret
- *(chart)* Point boopd at the existing redis password Secret
- *(chart)* Reject redis.auth.existingSecret without the subchart
- *(workflows)* CapacityWorkflow for runs.max_concurrent
- *(workflows)* Take a capacity lease before each run
- *(workflows)* Capacity metrics in the metric list
- *(chart)* [**breaking**] Render the runs ResourceQuota from maxConcurrent

### Bug Fixes

- *(release)* Sign the image and attest both artefacts
- *(release)* Stamp appVersion without the v
- *(chart)* Default the image tag to appVersion without a v
- *(release)* Print the published image tag in release notes

### Refactor

- *(workflows)* Share the lease book between entities

### Documentation

- *(config)* Add the boopd.hcl reference
- *(impl)* Add IMPL-0002 for issues 8-11
- How to verify a release's signatures and provenance
- *(chart)* Say the default image tag has no v
- *(chart)* GitOps installs and redis.auth.existingSecret
- *(config)* The chart's existingSecret redis wiring
- Runs.max_concurrent is a lease, the quota a backstop
- *(design)* Amend OQ12 for the capacity lease
- CapacityWorkflow in the package doc and CLAUDE.md
- *(impl)* IMPL-0002 Phase 5 tasks 5.1 and 5.2
- *(impl)* IMPL-0002 CI green; release checks deferred

### Testing

- *(chart)* Cover redis.auth.existingSecret
- *(e2e)* Redis with an existing password Secret
- *(workflows)* Cover the capacity lease
- *(e2e)* Runs.max_concurrent admits one run at a time

### Miscellaneous Tasks

- Check release.yml's SLSA image references
- *(chart)* Fail when two renders with an existing Secret differ

## [0.2.1] - 2026-10-12

### Bug Fixes

- *(activities)* Measure run overhead from the container's start

## [0.2.0] - 2026-10-12

### Features

- *(discovery)* [**breaking**] Probe every Renovate config file name and parse JSON5

### Documentation

- *(design-0001)* Amend OQ3 to probe every Renovate config file name

## [0.1.1] - 2026-10-12

### Bug Fixes

- *(just)* K3d-install arguments are positional and stay on the k3d context

### Documentation

- *(impl-0001)* Task 7.9 done against boop-test-repo

## [0.1.0] - 2026-10-11

### Features

- *(jobspec)* Port renovate-operator's Job builder for one repository per Job
- *(temporal)* Port repo-guardian's Temporal plumbing and the installation budget entity
- *(platform)* Carry repository id and node id from discovery
- *(platform)* Add an App-level client that lists installations
- *(platform)* Add the Minter seam with scoped mint and revoke
- *(platform)* Expose installation discovery page by page
- *(platform)* Probe for the config file over GraphQL, REST as fallback
- *(platform)* Read core and graphql from /rate_limit
- *(platform)* Derive the client limiter from the discovered limit
- *(platform)* Add CheckRepo with gone, no-config, present and error
- *(config)* Decode the HCL config file with hclkit
- *(config)* Apply defaults and validate every rule with positions
- *(config)* Read secret-backed values from mounted files
- *(profiles)* Resolve a repository's profile from extends and managers
- *(config)* Build jobspec inputs from config and add config validate
- *(kube)* Drive one Renovate Job through its lifecycle
- *(kube)* Reconnect the log follower and drop replayed lines
- *(kube)* Count API requests by verb, resource and code
- *(renovate)* Scan the run's log and parse the report
- *(platform)* Resolve repository ids by slug and re-tune a live limiter
- *(workflows)* Activity types for runs, discovery and CheckRepo
- *(activities)* ListInstallations, CheckRepo and ReadRateLimit
- *(activities)* DiscoverInstallation pages, probes and signals
- *(activities)* Classify a run by the design's table
- *(activities)* RunRenovate runs one repository as a Job
- *(observability)* Run metrics and the run_complete line
- *(activities)* Minter seam and registration under workflow names
- *(workflows)* DiscoveryWorkflow and the discovery schedules
- *(workflows)* RepoWorkflow owns one repository
- *(observability)* Logger, Prometheus exporter, health and budget gauges
- *(temporal)* Register missing search attributes on the namespace
- *(boopd)* Worker role with readiness, schedules and graceful stop
- *(chart)* Rename to boopd; the Deployment runs the worker role
- *(chart)* Render boopd.hcl from values into a mounted ConfigMap
- *(chart)* Worker Role and RoleBinding in the runs namespace
- *(chart)* Temporal connection values and worker-only credential mounts
- *(chart)* Restricted namespace, runs ResourceQuota and egress NetworkPolicies
- *(chart)* Redis subchart for Renovate's datasource cache
- *(just)* K3d-install takes a values file and the App key

### Bug Fixes

- *(chart)* Ct install against a bare cluster
- *(activities)* Heartbeat RunRenovate while the pod is pending
- *(observability)* Close the health server when shutdown runs out

### Documentation

- Review DESIGN-0001 and align ADRs with the spike design
- Rewrite DESIGN-0001 with diagrams, verified facts and lettered open questions
- Add OQ11 (App key custody, OpenBao deferred) and token revocation to DESIGN-0001
- ADR-0009 runs each Renovate run as a Kubernetes Job; rework DESIGN-0001 around it
- Record DESIGN-0001 decisions (a on all open questions except OQ8)
- Record OQ8 decision (report via the Renovate log line)
- Approve DESIGN-0001 and accept ADR-0009
- Mark spike step 4 done and add internal/jobspec to the layout
- Add IMPL-0001, the spike implementation plan with phases, criteria and open questions
- *(impl)* Record the IMPL-0001 decisions; config is HCL via hclkit, tests lean on k3d e2e
- *(impl)* Mark task 4.5 deferred until a scratch repository exists
- Phase 5 activities in the package doc and CLAUDE.md
- Phase 6 layout, worker role and boopd rename
- Phase 7 chart status and configuration contract
- *(impl-0001)* Fixture contents and phase 8 deferred to the homelab

### Testing

- *(platform)* Close IMPL-0001 Phase 2 and refresh the package docs
- *(config)* Golden config renders the jobspec fixture; close Phase 3
- *(e2e)* Add the stub Renovate image
- *(e2e)* Add the k3d e2e harness, just e2e and the E2E CI job
- *(e2e)* Run the kube lifecycle against k3d; read deadlines first
- *(kube)* Pin the create-suspended, Secret, unsuspend request order
- *(fakegithub)* In-process GitHub for activity tests
- *(e2e)* RunRenovate end to end in k3d
- *(activities)* A failed mint is an infrastructure error
- *(workflows)* Every convergence row and the RepoWorkflow paths
- *(e2e)* Worker discovery, run, recheck and graceful stop
- *(chart)* Golden config render and the run and budget alerts
- *(chart)* Keep the golden values out of ct install
- *(e2e)* Stub GitHub image and the chart e2e

### Miscellaneous Tasks

- Initial commit
- Bootstrap boop with founding docs and GitHub platform client
- Bump golangci-lint to 2.13.2 to match mise.toml
- Bump Go to 1.27.2 and golangci-lint to 2.14.0
- *(licenses)* Ignore nexus-proto-annotations, MIT upstream with no LICENSE in its module zip

