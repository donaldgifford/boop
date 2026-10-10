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

package workflows

import "time"

// Activity error types (temporal.ApplicationError.Type()) RunRenovate
// returns, so the workflow can tell its failure rows apart
// (DESIGN-0001 § RunRenovate activity, Classification).
const (
	// ErrTypePending: the pod was not Running within the pending
	// timeout; the Job was deleted. Release the lease and back off.
	ErrTypePending = "pending"
	// ErrTypeRateLimited: Renovate hit a rate limit. Details carry a
	// RateLimited with the retryAt for the installation.
	ErrTypeRateLimited = "rate-limited"
	// ErrTypeInfrastructure: everything else that is not the
	// repository's fault (token mint, API server, a run that ended
	// without a result).
	ErrTypeInfrastructure = "infrastructure"
)

// RateLimited is the details of an ErrTypeRateLimited error.
type RateLimited struct {
	RetryAt time.Time
}

// Outcome is a run's classification.
type Outcome string

// Outcomes (DESIGN-0001 § RunRenovate activity, Classification).
const (
	OutcomeSucceeded Outcome = "Succeeded"
	OutcomeSkipped   Outcome = "Skipped"
	OutcomeFailed    Outcome = "Failed"
	OutcomeTimedOut  Outcome = "TimedOut"
)

// RunInput is the RunRenovate activity input.
type RunInput struct {
	RepoID         int64
	Slug           string
	DefaultBranch  string
	InstallationID int64
	Endpoint       string // unset for api.github.com
	Profile        string // resolved by the workflow
	LeaseID        string
	Attempt        int    // part of the Job name
	DryRun         string // "" or "full"
}

// RunResult is the RunRenovate activity result.
type RunResult struct {
	Outcome          Outcome
	RepositoryResult string        // Renovate's result, e.g. "done", "disabled-no-config"
	ExitCode         int           // from the container's terminated state
	Duration         time.Duration // pod Running to container exit
	PodStart         time.Duration // Job creation to pod Running
	RenovateVersion  string
	Managers         []string      // keys of the report's packageFiles
	Tuples           []UpdateTuple // from the report
	Problems         []Problem     // repository-level problems from the report
	Progress         Progress      // this attempt only
	ReportMissing    bool          // tuples rebuilt from branch and PR events
	RateBefore       *Readings
	RateAfter        *Readings
}

// UpdateTuple is one row per upgrade in the report's
// branches[].upgrades[], with Manager recovered from packageFiles.
type UpdateTuple struct {
	BranchName     string
	BranchResult   string
	PRNumber       int
	Manager        string
	Datasource     string
	DepName        string
	PackageName    string
	PackageFile    string
	UpdateType     string
	CurrentVersion string
	NewVersion     string
}

// Problem is a repository-level problem from the report.
type Problem struct {
	Level   int // bunyan level: 40 warn, 50 error
	Message string
}

// Progress counts what one attempt changed.
type Progress struct {
	BranchesChanged int
	PRsChanged      int
}

// Discovered is the discovered signal DiscoverInstallation sends with
// SignalWithStart to repo/github/<id>.
type Discovered struct {
	Slug              string
	DefaultBranch     string
	InstallationID    int64
	DiscoveryInterval time.Duration
	Extends           []string
}

// RunSummary is the last run as RepoWorkflow keeps it.
type RunSummary struct {
	Outcome          Outcome
	RepositoryResult string
	FinishedAt       time.Time
	Progress         Progress
}

// RepoState is RepoWorkflow's input and its ContinueAsNew state
// (DESIGN-0001 § RepoWorkflow).
type RepoState struct {
	Platform          string
	RepoID            int64
	Slug              string
	DefaultBranch     string
	InstallationID    int64
	DiscoveryInterval time.Duration
	Extends           []string
	Managers          []string
	Profile           string
	LastSeen          time.Time
	LastRun           *RunSummary
	NextDue           time.Time
	StallCount        int
	ConsecutiveReruns int
	Iterations        int
}

// ListInstallationsInput is the ListInstallations activity input.
type ListInstallationsInput struct {
	App string
}

// InstallationInfo is one installation ListInstallations returns.
type InstallationInfo struct {
	ID        int64
	Account   string
	Suspended bool
}

// DiscoverInput is the DiscoverInstallation activity input.
type DiscoverInput struct {
	App            string
	InstallationID int64
}

// DiscoverProgress is DiscoverInstallation's heartbeat; a retry resumes
// after Page.
type DiscoverProgress struct {
	Page      int
	Seen      int
	Onboarded int
}

// DiscoverSummary is DiscoverInstallation's result.
type DiscoverSummary struct {
	InstallationID int64
	Pages          int
	Seen           int
	Onboarded      int
	ProbeCost      int
}

// CheckRepoInput is the CheckRepo activity input.
type CheckRepoInput struct {
	InstallationID int64
	RepoID         int64
}

// CheckRepoResult is CheckRepo's answer. State is "gone", "no-config" or
// "present"; an outage is an activity error, never an answer.
type CheckRepoResult struct {
	State         string
	Slug          string
	DefaultBranch string
}

// CheckRepo states.
const (
	RepoGone     = "gone"
	RepoNoConfig = "no-config"
	RepoPresent  = "present"
)
