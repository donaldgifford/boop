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

package ingest

import (
	"context"
	"log/slog"
	"time"
)

// Outcome is what ingest did with a delivery.
type Outcome string

// Outcomes (DESIGN-0003 § Recording deliveries).
const (
	// OutcomeRouted: at least one Temporal action took effect.
	OutcomeRouted Outcome = "routed"
	// OutcomeIgnored: nothing to do, or every signal found no workflow.
	OutcomeIgnored Outcome = "ignored"
	// OutcomeOwn: sent by the App's own bot.
	OutcomeOwn Outcome = "own"
	// OutcomeRejected: unknown App, bad signature, too large, or a body
	// that does not decode.
	OutcomeRejected Outcome = "rejected"
	// OutcomeFailed: a Temporal call failed; GitHub got a 503.
	OutcomeFailed Outcome = "failed"
)

// Record is one delivery as ingest saw it (DESIGN-0003 § Recording
// deliveries). A rejected delivery keeps only its headers: an unverified
// body is never read into a record.
type Record struct {
	DeliveryID     string
	App            string
	InstallationID int64
	RepoID         int64
	RepoSlug       string
	Event          string
	Action         string
	SenderLogin    string
	SenderType     string
	Own            bool
	Subject        string
	ReceivedAt     time.Time
	Outcome        Outcome
	// Status is the HTTP status ingest answered.
	Status int
	// Actions are the Temporal actions taken.
	Actions []ActionRecord
	// Error is why a delivery was rejected.
	Error string
}

// ActionRecord is one Temporal action and how it went.
type ActionRecord struct {
	Kind       Kind
	WorkflowID string
	Signal     string
	// NotFound: a signal found no running workflow.
	NotFound bool
	Error    string
}

// Sink receives every delivery's record after the answer is decided.
// It must not block: DESIGN-0004's store replaces LogSink.
type Sink interface {
	Record(ctx context.Context, r *Record)
}

// LogSink writes one info line per delivery.
type LogSink struct {
	Logger *slog.Logger
}

// Record implements Sink.
func (s LogSink) Record(ctx context.Context, r *Record) {
	attrs := []slog.Attr{
		slog.String("delivery_id", r.DeliveryID),
		slog.String("app", r.App),
		slog.String("event", r.Event),
		slog.String("outcome", string(r.Outcome)),
		slog.Int("status", r.Status),
		slog.Time("received_at", r.ReceivedAt),
	}
	if r.Outcome == OutcomeRejected {
		attrs = append(attrs, slog.String("error", r.Error))
		s.Logger.LogAttrs(ctx, slog.LevelInfo, "delivery", attrs...)
		return
	}
	attrs = append(attrs,
		slog.String("action", r.Action),
		slog.Int64("installation_id", r.InstallationID),
		slog.Int64("repo_id", r.RepoID),
		slog.String("repo_slug", r.RepoSlug),
		slog.String("sender_login", r.SenderLogin),
		slog.String("sender_type", r.SenderType),
		slog.Bool("own", r.Own),
		slog.String("subject", r.Subject),
		slog.Any("actions", r.Actions),
	)
	s.Logger.LogAttrs(ctx, slog.LevelInfo, "delivery", attrs...)
}
