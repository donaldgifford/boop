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
	"errors"
	"fmt"
	"time"
)

// Kind is an event kind: a dotted open enum (DESIGN-0004 § Event
// catalogue). Readers must tolerate kinds they do not know.
type Kind string

// The v1 catalogue.
const (
	KindDeliveryReceived    Kind = "delivery.received"
	KindDeliveryFailed      Kind = "delivery.failed"
	KindDeliveryMissed      Kind = "delivery.missed"
	KindDeliveryRedelivered Kind = "delivery.redelivered"

	KindPROpened       Kind = "pr.opened"
	KindPREdited       Kind = "pr.edited"
	KindPRClosed       Kind = "pr.closed"
	KindPRMerged       Kind = "pr.merged"
	KindPRReopened     Kind = "pr.reopened"
	KindPRSynchronized Kind = "pr.synchronized"
	KindPRSynced       Kind = "pr.synced"
	KindPRCheckbox     Kind = "pr.checkbox"

	KindDashboardUpdated Kind = "dashboard.updated"
	KindDashboardTicked  Kind = "dashboard.ticked"

	KindRepoOnboarded    Kind = "repo.onboarded"
	KindRepoOffboarded   Kind = "repo.offboarded"
	KindRepoRefreshed    Kind = "repo.refreshed"
	KindRepoRunScheduled Kind = "repo.run-scheduled"

	KindRunStarted         Kind = "run.started"
	KindRunFinished        Kind = "run.finished"
	KindRunStalled         Kind = "run.stalled"
	KindRunCapacityDenied  Kind = "run.capacity-denied"
	KindRunBudgetWait      Kind = "run.budget-wait"
	KindInstallationAdded  Kind = "installation.added"
	KindInstallationSusp   Kind = "installation.suspended"
	KindInstallationUnsusp Kind = "installation.unsuspended"
	KindInstallationRemove Kind = "installation.removed"

	KindDiscoveryFinished Kind = "discovery.finished"
	KindActionRecheck     Kind = "action.recheck"
)

// Source is who wrote an event.
type Source string

// The event sources.
const (
	SourceWebhook Source = "webhook"
	SourceWorker  Source = "worker"
	SourceAudit   Source = "audit"
	SourceAPI     Source = "api"
)

// Actor types.
const (
	ActorUser   = "user"
	ActorBot    = "bot"
	ActorBoopd  = "boopd"
	ActorOIDC   = "oidc"
	ActorGitHub = "github"
)

// Actor is who caused an event: a GitHub user or bot, a boopd role, or an
// OIDC subject for an API action. The zero Actor is none.
type Actor struct {
	Login string
	Type  string
}

// Event is one entry of the log. Subject IDs are zero when they do not
// apply; GitHub never uses zero for an installation or repository.
type Event struct {
	// Key is the idempotency key (OQ7): a second Append of the same key is
	// a no-op, apply included.
	Key string
	// OccurredAt is when it happened at the source.
	OccurredAt     time.Time
	Kind           Kind
	Source         Source
	InstallationID int64
	RepoID         int64
	PRNumber       int
	Actor          Actor
	// CauseKey is the Key of the event that caused this one, if any.
	CauseKey string
	// Summary is one human line, as shown in timelines.
	Summary string
	// Data is the kind's typed payload; its Kind must be the event's.
	Data Data
}

// Data is a kind's typed payload. Each carries V, its own version, so a
// payload can change shape without a new kind.
type Data interface {
	// Version is the payload's V.
	Version() int
}

// ErrUnknownKind is returned for a kind with no entry in the catalogue.
var ErrUnknownKind = errors.New("unknown event kind")

// kinds is the catalogue (OQ5): each kind's payload type. Applies are in
// applies, keyed the same way.
var kinds = map[Kind]func() Data{
	KindDeliveryReceived:    func() Data { return &DeliveryReceived{} },
	KindDeliveryFailed:      func() Data { return &DeliveryAudited{} },
	KindDeliveryMissed:      func() Data { return &DeliveryAudited{} },
	KindDeliveryRedelivered: func() Data { return &DeliveryAudited{} },

	KindPROpened:       func() Data { return &PullRequest{} },
	KindPREdited:       func() Data { return &PullRequest{} },
	KindPRClosed:       func() Data { return &PullRequest{} },
	KindPRMerged:       func() Data { return &PullRequest{} },
	KindPRReopened:     func() Data { return &PullRequest{} },
	KindPRSynchronized: func() Data { return &PullRequest{} },
	KindPRSynced:       func() Data { return &PullRequest{} },
	KindPRCheckbox:     func() Data { return &PRCheckbox{} },

	KindDashboardUpdated: func() Data { return &DashboardUpdated{} },
	KindDashboardTicked:  func() Data { return &DashboardTicked{} },

	KindRepoOnboarded:    func() Data { return &Repository{} },
	KindRepoOffboarded:   func() Data { return &Repository{} },
	KindRepoRefreshed:    func() Data { return &Repository{} },
	KindRepoRunScheduled: func() Data { return &RunScheduled{} },

	KindRunStarted:        func() Data { return &RunStarted{} },
	KindRunFinished:       func() Data { return &RunFinished{} },
	KindRunStalled:        func() Data { return &RepoState{} },
	KindRunCapacityDenied: func() Data { return &RepoState{} },
	KindRunBudgetWait:     func() Data { return &RepoState{} },

	KindInstallationAdded:  func() Data { return &Installation{} },
	KindInstallationSusp:   func() Data { return &Installation{} },
	KindInstallationUnsusp: func() Data { return &Installation{} },
	KindInstallationRemove: func() Data { return &Installation{} },

	KindDiscoveryFinished: func() Data { return &DiscoveryFinished{} },
	KindActionRecheck:     func() Data { return &Recheck{} },
}

// Kinds returns every catalogue kind, for tests and documentation.
func Kinds() []Kind {
	out := make([]Kind, 0, len(kinds))
	for k := range kinds {
		out = append(out, k)
	}
	return out
}

// DecodeData decodes a stored payload into the kind's type.
func DecodeData(kind Kind, raw []byte) (Data, error) {
	newData, ok := kinds[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}
	d := newData()
	if err := json.Unmarshal(raw, d); err != nil {
		return nil, fmt.Errorf("decode %s payload: %w", kind, err)
	}
	return d, nil
}
