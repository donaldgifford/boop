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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/boop/internal/store/db"
)

// Append outcomes, the outcome label of boopd_store_append_total.
const (
	OutcomeApplied   = "applied"
	OutcomeDuplicate = "duplicate"
	OutcomeError     = "error"
)

// Metrics records the store's metrics. observability.Metrics implements
// it.
type Metrics interface {
	// StoreAppend counts one event by role, kind and outcome.
	StoreAppend(role, kind, outcome string)
	// StoreAppendSeconds records one Append transaction.
	StoreAppendSeconds(role string, d time.Duration)
}

// Store appends events and applies them to the projections.
type Store struct {
	pool    *pgxpool.Pool
	role    string
	metrics Metrics
}

// New returns a Store on pool. role is the database role the pool logs in
// as, used only as a metric label; metrics may be nil.
func New(pool *pgxpool.Pool, role string, metrics Metrics) *Store {
	return &Store{pool: pool, role: role, metrics: metrics}
}

// applyFunc applies one event to the projections, inside the Append or
// rebuild transaction.
type applyFunc func(ctx context.Context, q *db.Queries, e *Event) error

// applies holds each kind's projection apply (DESIGN-0004 § Event
// catalogue). A kind with no entry, pr.checkbox and action.recheck, is
// timeline-only: its event is recorded and nothing else changes.
var applies = map[Kind]applyFunc{
	KindDeliveryReceived:    applyDeliveryReceived,
	KindDeliveryFailed:      applyDeliveryAudited,
	KindDeliveryMissed:      applyDeliveryAudited,
	KindDeliveryRedelivered: applyDeliveryAudited,

	KindPROpened:       applyPullRequest,
	KindPREdited:       applyPullRequest,
	KindPRClosed:       applyPullRequest,
	KindPRMerged:       applyPullRequest,
	KindPRReopened:     applyPullRequest,
	KindPRSynchronized: applyPullRequest,
	KindPRSynced:       applyPullRequest,

	KindDashboardUpdated: applyDashboardUpdated,
	KindDashboardTicked:  applyDashboardTicked,

	KindRepoOnboarded:    applyRepository,
	KindRepoOffboarded:   applyRepository,
	KindRepoRefreshed:    applyRepository,
	KindRepoRunScheduled: applyRunScheduled,

	KindRunStarted:        applyRunStarted,
	KindRunFinished:       applyRunFinished,
	KindRunStalled:        applyRepoState,
	KindRunCapacityDenied: applyRepoState,
	KindRunBudgetWait:     applyRepoState,

	KindInstallationAdded:  applyInstallation,
	KindInstallationSusp:   applyInstallation,
	KindInstallationUnsusp: applyInstallation,
	KindInstallationRemove: applyInstallation,

	KindDiscoveryFinished: applyDiscoveryFinished,
}

// apply runs e's apply, if its kind has one.
func apply(ctx context.Context, q *db.Queries, e *Event) error {
	fn, ok := applies[e.Kind]
	if !ok {
		return nil
	}
	if err := fn(ctx, q, e); err != nil {
		return fmt.Errorf("apply %s %s: %w", e.Kind, e.Key, err)
	}
	return nil
}

// Append records events and applies each to the projections in one
// transaction. An event whose Key is already recorded is skipped, apply
// included, so writers may retry freely. Either every event commits or
// none does.
func (s *Store) Append(ctx context.Context, events ...Event) (err error) {
	if len(events) == 0 {
		return nil
	}
	for i := range events {
		if err := validate(&events[i]); err != nil {
			return err
		}
	}

	start := time.Now()
	outcomes := make([]string, len(events))
	defer func() {
		s.record(events, outcomes, err, time.Since(start))
	}()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin append: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx) //nolint:errcheck // the append error is the one to report
		}
	}()

	if err := lockRepositories(ctx, tx, events); err != nil {
		return err
	}
	for i := range events {
		appended, err := appendOne(ctx, tx, &events[i])
		if err != nil {
			return err
		}
		outcomes[i] = OutcomeDuplicate
		if appended {
			outcomes[i] = OutcomeApplied
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit append: %w", err)
	}
	return nil
}

// repoLockSpace is the first key of the per-repository advisory locks
// ("boop"), so they cannot meet another user's single-key locks.
const repoLockSpace = 0x626f6f70

// lockRepositories serializes appends per repository for the rest of
// the transaction. A workflow records without waiting, so two of its
// appends can run at once; without the lock neither would see the
// other's rows (repo.run-scheduled landing beside the repo.onboarded
// that creates the row). The locks are taken in repository order, so
// appends spanning several repositories cannot deadlock.
func lockRepositories(ctx context.Context, tx pgx.Tx, events []Event) error {
	ids := make([]int64, 0, len(events))
	for i := range events {
		if events[i].RepoID != 0 {
			ids = append(ids, events[i].RepoID)
		}
	}
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1::int, hashtext($2::bigint::text))`, repoLockSpace, id); err != nil {
			return fmt.Errorf("lock repository %d: %w", id, err)
		}
	}
	return nil
}

// appendOne inserts one event and applies it. It reports false for a
// duplicate key.
func appendOne(ctx context.Context, tx pgx.Tx, e *Event) (bool, error) {
	var key string
	err := tx.QueryRow(ctx,
		`INSERT INTO event_keys (key, occurred_at) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING RETURNING key`,
		e.Key, e.OccurredAt).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("record key %s: %w", e.Key, err)
	}

	data, err := json.Marshal(e.Data)
	if err != nil {
		return false, fmt.Errorf("encode %s payload: %w", e.Kind, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO events
	(key, occurred_at, kind, source, installation_id, repo_id, pr_number,
	 actor_login, actor_type, cause_key, summary, data)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		e.Key, e.OccurredAt, string(e.Kind), string(e.Source),
		nullInt64(e.InstallationID), nullInt64(e.RepoID), nullInt32(e.PRNumber),
		nullText(e.Actor.Login), nullText(e.Actor.Type), nullText(e.CauseKey),
		e.Summary, data,
	); err != nil {
		return false, fmt.Errorf("insert event %s: %w", e.Key, err)
	}

	if err := apply(ctx, db.New(tx), e); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) record(events []Event, outcomes []string, err error, d time.Duration) {
	if s.metrics == nil {
		return
	}
	for i := range events {
		outcome := outcomes[i]
		if err != nil {
			outcome = OutcomeError
		}
		s.metrics.StoreAppend(s.role, string(events[i].Kind), outcome)
	}
	s.metrics.StoreAppendSeconds(s.role, d)
}

// validate reports an event Append cannot record: no key, no time, or a
// payload missing, unversioned or of another kind's type.
func validate(e *Event) error {
	if e.Key == "" {
		return fmt.Errorf("event %s has no key", e.Kind)
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("event %s has no occurred_at", e.Key)
	}
	newData, ok := kinds[e.Kind]
	if !ok {
		return fmt.Errorf("event %s: %w: %q", e.Key, ErrUnknownKind, e.Kind)
	}
	if e.Data == nil {
		return fmt.Errorf("event %s (%s) has no data", e.Key, e.Kind)
	}
	if want := newData(); fmt.Sprintf("%T", want) != fmt.Sprintf("%T", e.Data) {
		return fmt.Errorf("event %s (%s) has data %T, want %T", e.Key, e.Kind, e.Data, want)
	}
	if e.Data.Version() < 1 {
		return fmt.Errorf("event %s (%s) has data version %d, want 1 or more", e.Key, e.Kind, e.Data.Version())
	}
	return nil
}

func nullInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func nullInt32(v int) *int32 {
	if v == 0 {
		return nil
	}
	n := i32(v)
	return &n
}

// i32 narrows a count or number that fits in a Postgres integer.
func i32(v int) int32 {
	return int32(v) //nolint:gosec // G115: PR numbers, attempts and counts fit in int32
}

// payload returns e's Data as T. Append's validate has checked the type,
// so a mismatch here is a programming error, still reported, not a panic.
func payload[T Data](e *Event) (T, error) {
	d, ok := e.Data.(T)
	if !ok {
		return d, fmt.Errorf("event %s (%s) has data %T", e.Key, e.Kind, e.Data)
	}
	return d, nil
}

// jsonValue encodes v for a NOT NULL jsonb column, as empty when v is
// nil.
func jsonValue(v any, empty string) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if string(b) == "null" {
		return json.RawMessage(empty), nil
	}
	return b, nil
}

func nullText(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
