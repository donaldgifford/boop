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

### Miscellaneous Tasks

- Initial commit
- Bootstrap boop with founding docs and GitHub platform client
- Bump golangci-lint to 2.13.2 to match mise.toml
- Bump Go to 1.27.2 and golangci-lint to 2.14.0
- *(licenses)* Ignore nexus-proto-annotations, MIT upstream with no LICENSE in its module zip

