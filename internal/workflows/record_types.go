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

// Event kinds the workflows record (DESIGN-0004 § Event catalogue). They
// are the store's kind strings; workflows do not import the store, and a
// test in the activities package keeps the two in step.
const (
	KindRepoOnboarded     = "repo.onboarded"
	KindRepoOffboarded    = "repo.offboarded"
	KindRepoRefreshed     = "repo.refreshed"
	KindRepoRunScheduled  = "repo.run-scheduled"
	KindRunStalled        = "run.stalled"
	KindRunCapacityDenied = "run.capacity-denied"
	KindRunBudgetWait     = "run.budget-wait"
	KindActionRecheck     = "action.recheck"
	KindDiscoveryFinished = "discovery.finished"
)

// Onboarding paths, RecordRepository.Via.
const (
	ViaDiscovery = "discovery"
	ViaWebhook   = "webhook"
)

// RecordEventsInput is the RecordEvents activity input: workflow
// decisions to append, each with its own key.
type RecordEventsInput struct {
	Events []RecordedEvent
}

// RecordedEvent is one decision. Exactly one of the payload pointers is
// set, the one its Kind uses.
type RecordedEvent struct {
	// Key is wf:<workflow id>:<run id>:<seq>.
	Key            string
	Kind           string
	OccurredAt     time.Time
	InstallationID int64
	RepoID         int64
	// CauseKey is the key of the event that led here, if any.
	CauseKey string
	Summary  string
	// Actor is who asked, for an action.
	Actor *RecordActor

	Repository *RecordRepository
	Scheduled  *RecordScheduled
	State      *RecordState
	Recheck    *RecordRecheck
	Discovery  *RecordDiscovery
}

// RecordActor is a recorded event's actor.
type RecordActor struct {
	Login string
	Type  string
}

// RecordRepository is repo.onboarded, repo.offboarded and
// repo.refreshed.
type RecordRepository struct {
	Slug    string
	Profile string
	// Via is how an onboarding was found: discovery or webhook.
	Via string
	// Reason is why an offboarding happened: gone, no-config, a
	// Renovate result, or a webhook's.
	Reason string
}

// RecordScheduled is repo.run-scheduled.
type RecordScheduled struct {
	NextDue  time.Time
	Priority Priority
	// Reason is the event's, empty for the cadence.
	Reason string
}

// RecordState is run.stalled, run.capacity-denied and run.budget-wait.
type RecordState struct {
	Reason string
	Until  time.Time
}

// RecordRecheck is action.recheck.
type RecordRecheck struct {
	RequestID string
}

// RecordDiscovery is discovery.finished.
type RecordDiscovery struct {
	App        string
	StartedAt  time.Time
	FinishedAt time.Time
	Seen       int
	Onboarded  int
	Failed     int
	ProbeCost  int
}

// RecordRunInput is the RecordRun activity input: one attempt with an
// outcome. Result is nil for a timeout.
type RecordRunInput struct {
	// RunID is <workflow run id>:<attempt>, the runs row's id.
	RunID          string
	RepoID         int64
	InstallationID int64
	Slug           string
	// CauseKey is the key of the run-scheduled or recheck event that
	// started the run.
	CauseKey   string
	QueuedAt   time.Time
	StartedAt  time.Time
	FinishedAt time.Time
	Outcome    Outcome
	Result     *RunResult
}
