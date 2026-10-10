/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package workflows holds boopd's Temporal workflow code (DESIGN-0001 §
// Workflows). Workflow code must be deterministic, so this package never
// imports the GitHub client, the Kubernetes client or the activities
// package; a depguard rule enforces that. Everything with side effects
// runs in the activities package, which workflows reach by the names
// below.
//
// The budget entity (installation.go), the priority and fairness
// presets and the name-only coupling are copied from repo-guardian
// internal/workflows at d1f20a0 (feat/impl-0028-controls-foundations)
// and changed so the budget is discovered from /rate_limit and tracks
// more than one resource (ADR-0008, DESIGN-0001 § InstallationWorkflow).
// The package moves to donaldgifford/x in Phase 0 (ADR-0007).
package workflows

import "strconv"

// Workflow type names. They are part of every running execution's
// identity, so renaming one strands the executions already started
// under the old name.
const (
	RepoWorkflowName         = "RepoWorkflow"
	InstallationWorkflowName = "InstallationWorkflow"
	DiscoveryWorkflowName    = "DiscoveryWorkflow"
)

// Activity names (DESIGN-0001 § Topology and packages). The activities
// package registers under these names and workflows execute by them, so
// neither side imports the other.
const (
	ListInstallationsActivity    = "ListInstallations"
	DiscoverInstallationActivity = "DiscoverInstallation"
	CheckRepoActivity            = "CheckRepo"
	ReadRateLimitActivity        = "ReadRateLimit"
	AcquireBudgetActivity        = "AcquireBudget"
	PlanRunActivity              = "PlanRun"
	RunRenovateActivity          = "RunRenovate"
)

// Signal names accepted by RepoWorkflow (DESIGN-0001 § RepoWorkflow).
const (
	DiscoveredSignal = "discovered"
	RecheckSignal    = "recheck"
)

// InstallationWorkflow's handlers: acquire is an Update, so the caller
// gets a synchronous grant or wait; report and suspend are Signals.
const (
	AcquireUpdate = "acquire"
	ReportSignal  = "report"
	SuspendSignal = "suspend"
)

// StateQuery is the query every entity workflow answers for debugging
// in the Temporal UI: InstallationWorkflow with its BudgetState,
// RepoWorkflow with its RepoState.
const StateQuery = "state"

// Platform is the one platform boopd runs against (RFC-0001). It is the
// second segment of every entity workflow ID (ADR-0002).
const Platform = "github"

// RepoWorkflowID is repo/github/<repository id>: the per-repository
// lock. One RepoWorkflow per repository, kept across renames and
// transfers because GitHub's numeric id never changes.
func RepoWorkflowID(repoID int64) string {
	return "repo/" + Platform + "/" + strconv.FormatInt(repoID, 10)
}

// InstallationWorkflowID is installation/github/<installation id>, the
// installation's rate budget.
func InstallationWorkflowID(installationID int64) string {
	return "installation/" + Platform + "/" + strconv.FormatInt(installationID, 10)
}

// DiscoveryScheduleID is discovery/<app name>: one Temporal Schedule per
// configured GitHub App (DESIGN-0001 § DiscoveryWorkflow).
func DiscoveryScheduleID(app string) string {
	return "discovery/" + app
}

// DiscoveryWorkflowID is the workflow ID the Schedule's action starts.
// The server appends the fire time, giving ADR-0002's
// discovery/<platform>/<schedule time>.
func DiscoveryWorkflowID() string {
	return "discovery/" + Platform
}
