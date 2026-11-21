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

package store

import (
	"encoding/json"
	"time"
)

// The payloads. Every field a writer knows is here, so the log alone can
// rebuild the projections; V starts at 1.

// DeliveryReceived is delivery.received: one delivery ingest answered.
type DeliveryReceived struct {
	V          int    `json:"v"`
	DeliveryID string `json:"delivery_id"`
	Attempt    int    `json:"attempt"`
	App        string `json:"app"`
	Event      string `json:"event"`
	Action     string `json:"action,omitempty"`
	// Outcome is ingest's: routed, ignored, rejected, and so on.
	Outcome string `json:"outcome"`
	// Payload is the decoded subset kept for 30 days (OQ7).
	Payload json.RawMessage `json:"payload,omitempty"`
}

// DeliveryAudited is delivery.failed, delivery.missed and
// delivery.redelivered: what the delivery audit read from GitHub.
type DeliveryAudited struct {
	V            int    `json:"v"`
	DeliveryID   string `json:"delivery_id"`
	Attempt      int    `json:"attempt"`
	App          string `json:"app"`
	Event        string `json:"event"`
	Action       string `json:"action,omitempty"`
	GitHubStatus int    `json:"github_status"`
	// Missed is set when GitHub delivered it and ingest has no record.
	Missed bool `json:"missed,omitempty"`
}

// PullRequest is every pr.* kind but pr.checkbox: the PR as GitHub last
// described it. UpdatedAt is GitHub's updated_at, the order guard.
type PullRequest struct {
	V         int        `json:"v"`
	Number    int        `json:"number"`
	Branch    string     `json:"branch"`
	State     string     `json:"state"`
	Title     string     `json:"title"`
	Labels    []string   `json:"labels,omitempty"`
	Automerge bool       `json:"automerge,omitempty"`
	URL       string     `json:"url,omitempty"`
	OpenedAt  *time.Time `json:"opened_at,omitempty"`
	MergedAt  *time.Time `json:"merged_at,omitempty"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// PR states.
const (
	PRStateOpen   = "open"
	PRStateMerged = "merged"
	PRStateClosed = "closed"
)

// PRCheckbox is pr.checkbox: a human ticked rebase or retry on a PR.
type PRCheckbox struct {
	V      int    `json:"v"`
	Number int    `json:"number"`
	Box    string `json:"box"`
}

// DashboardItem is one checkbox on the dependency dashboard.
type DashboardItem struct {
	Marker  string   `json:"marker"`
	Branch  string   `json:"branch"`
	Section string   `json:"section"`
	Title   string   `json:"title"`
	Deps    []string `json:"deps,omitempty"`
}

// DashboardUpdated is dashboard.updated: the bot rewrote the dashboard,
// and these are all of its items.
type DashboardUpdated struct {
	V     int             `json:"v"`
	Items []DashboardItem `json:"items"`
}

// DashboardTicked is dashboard.ticked: a human ticked one item.
type DashboardTicked struct {
	V      int    `json:"v"`
	Marker string `json:"marker"`
	Branch string `json:"branch"`
}

// Repository is repo.onboarded, repo.offboarded and repo.refreshed.
type Repository struct {
	V int `json:"v"`
	// Owner is the installation's account login; Slug is owner/name.
	Owner   string `json:"owner"`
	Slug    string `json:"slug"`
	Profile string `json:"profile,omitempty"`
	// Via is how an onboarding was found: discovery or webhook.
	Via string `json:"via,omitempty"`
	// Reason is why an offboarding happened.
	Reason string `json:"reason,omitempty"`
}

// RunScheduled is repo.run-scheduled.
type RunScheduled struct {
	V        int       `json:"v"`
	NextDue  time.Time `json:"next_due"`
	Priority string    `json:"priority,omitempty"`
	// Reason is the event that asked for the run; empty for the cadence.
	Reason string `json:"reason,omitempty"`
}

// RunStarted is run.started.
type RunStarted struct {
	V     int    `json:"v"`
	RunID string `json:"run_id"`
	Owner string `json:"owner"`
	Slug  string `json:"slug"`
	// QueuedMS is from the run's admission to its start.
	QueuedMS int64 `json:"queued_ms,omitempty"`
}

// Spend is one resource's budget before and after a run.
type Spend struct {
	Before int `json:"before"`
	After  int `json:"after"`
}

// BranchUpdate is one proposed update from the run's report.
type BranchUpdate struct {
	Branch      string `json:"branch"`
	PRNumber    int    `json:"pr_number,omitempty"`
	PackageFile string `json:"package_file"`
	PackageName string `json:"package_name"`
	DepName     string `json:"dep_name,omitempty"`
	Manager     string `json:"manager"`
	Datasource  string `json:"datasource"`
	UpdateType  string `json:"update_type,omitempty"`
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
}

// Dependency is one dependency the run's report found.
type Dependency struct {
	Manager        string `json:"manager"`
	PackageFile    string `json:"package_file"`
	PackageName    string `json:"package_name"`
	DepName        string `json:"dep_name,omitempty"`
	Datasource     string `json:"datasource"`
	CurrentValue   string `json:"current_value,omitempty"`
	CurrentVersion string `json:"current_version,omitempty"`
}

// Run outcomes, as workflows.Outcome names them. Failed and TimedOut
// leave the repository errored.
const (
	RunSucceeded = "Succeeded"
	RunSkipped   = "Skipped"
	RunFailed    = "Failed"
	RunTimedOut  = "TimedOut"
)

// RunFinished is run.finished.
type RunFinished struct {
	V                int              `json:"v"`
	RunID            string           `json:"run_id"`
	Owner            string           `json:"owner"`
	Slug             string           `json:"slug"`
	StartedAt        time.Time        `json:"started_at"`
	FinishedAt       time.Time        `json:"finished_at"`
	Outcome          string           `json:"outcome"`
	RepositoryResult string           `json:"repository_result,omitempty"`
	RenovateVersion  string           `json:"renovate_version,omitempty"`
	Spend            map[string]Spend `json:"spend,omitempty"`
	Problems         []string         `json:"problems,omitempty"`
	// ReportMissing keeps the previous updates and dependencies.
	ReportMissing bool           `json:"report_missing,omitempty"`
	Updates       []BranchUpdate `json:"updates,omitempty"`
	Dependencies  []Dependency   `json:"dependencies,omitempty"`
}

// Repository states.
const (
	RepoStateIdle    = "idle"
	RepoStateRunning = "running"
	RepoStateWaiting = "waiting"
	// RepoStateRateLimited is waiting for the installation's budget.
	RepoStateRateLimited = "rate-limited"
	RepoStateStalled     = "stalled"
	RepoStateErrored     = "errored"
)

// RepoState is run.stalled, run.capacity-denied and run.budget-wait.
type RepoState struct {
	V      int        `json:"v"`
	Reason string     `json:"reason,omitempty"`
	Until  *time.Time `json:"until,omitempty"`
}

// Installation is every installation.* kind.
type Installation struct {
	V            int    `json:"v"`
	App          string `json:"app"`
	AccountLogin string `json:"account_login"`
	AccountType  string `json:"account_type,omitempty"`
}

// DiscoveryFinished is discovery.finished: one pass's result.
type DiscoveryFinished struct {
	V          int       `json:"v"`
	App        string    `json:"app"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Seen       int       `json:"seen"`
	Onboarded  int       `json:"onboarded"`
	Failed     int       `json:"failed"`
	ProbeCost  int       `json:"probe_cost"`
}

// Recheck is action.recheck: the actor and request carried in the signal.
type Recheck struct {
	V         int    `json:"v"`
	RequestID string `json:"request_id,omitempty"`
}

// Version implements Data.
func (d *DeliveryReceived) Version() int { return d.V }

// Version implements Data.
func (d *DeliveryAudited) Version() int { return d.V }

// Version implements Data.
func (d *PullRequest) Version() int { return d.V }

// Version implements Data.
func (d *PRCheckbox) Version() int { return d.V }

// Version implements Data.
func (d *DashboardUpdated) Version() int { return d.V }

// Version implements Data.
func (d *DashboardTicked) Version() int { return d.V }

// Version implements Data.
func (d *Repository) Version() int { return d.V }

// Version implements Data.
func (d *RunScheduled) Version() int { return d.V }

// Version implements Data.
func (d *RunStarted) Version() int { return d.V }

// Version implements Data.
func (d *RunFinished) Version() int { return d.V }

// Version implements Data.
func (d *RepoState) Version() int { return d.V }

// Version implements Data.
func (d *Installation) Version() int { return d.V }

// Version implements Data.
func (d *DiscoveryFinished) Version() int { return d.V }

// Version implements Data.
func (d *Recheck) Version() int { return d.V }
