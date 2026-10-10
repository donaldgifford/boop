# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
this project adheres to [Semantic Versioning](https://semver.org/).
## [unreleased]

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

### Testing

- *(platform)* Close IMPL-0001 Phase 2 and refresh the package docs
- *(config)* Golden config renders the jobspec fixture; close Phase 3
- *(e2e)* Add the stub Renovate image
- *(e2e)* Add the k3d e2e harness, just e2e and the E2E CI job
- *(e2e)* Run the kube lifecycle against k3d; read deadlines first
- *(kube)* Pin the create-suspended, Secret, unsuspend request order
- *(fakegithub)* In-process GitHub for activity tests

### Miscellaneous Tasks

- Initial commit
- Bootstrap boop with founding docs and GitHub platform client
- Bump golangci-lint to 2.13.2 to match mise.toml
- Bump Go to 1.27.2 and golangci-lint to 2.14.0
- *(licenses)* Ignore nexus-proto-annotations, MIT upstream with no LICENSE in its module zip

