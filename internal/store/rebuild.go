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
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/boop/internal/store/db"
)

// DefaultRebuildBatch is how many events Rebuild reads at a time.
const DefaultRebuildBatch = 1000

// RebuildResult is what Rebuild replayed.
type RebuildResult struct {
	// Events is how many events went through an apply or were
	// timeline-only.
	Events int
	// Unknown is how many were of a kind this boopd does not know, and
	// were skipped.
	Unknown int
}

// Rebuild truncates the projections and replays the log through the same
// applies Append uses, in (occurred_at, id) order, batch events at a
// time. It runs in one transaction as the owner: writers block on the
// truncated tables until it commits, then apply on top, so nothing is
// lost or applied twice.
func Rebuild(ctx context.Context, pool *pgxpool.Pool, batch int, logger *slog.Logger) (res RebuildResult, err error) {
	if batch <= 0 {
		batch = DefaultRebuildBatch
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("begin rebuild: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx) //nolint:errcheck // the rebuild error is the one to report
		}
	}()
	q := db.New(tx)
	if err := q.TruncateProjections(ctx); err != nil {
		return res, fmt.Errorf("truncate projections: %w", err)
	}

	after := db.ListEventsAfterParams{Batch: i32(batch)}
	for {
		rows, err := q.ListEventsAfter(ctx, after)
		if err != nil {
			return res, fmt.Errorf("read events: %w", err)
		}
		for i := range rows {
			e, ok, err := eventOf(&rows[i])
			if err != nil {
				return res, err
			}
			if !ok {
				res.Unknown++
				continue
			}
			if err := apply(ctx, q, &e); err != nil {
				return res, err
			}
			res.Events++
		}
		if len(rows) < batch {
			break
		}
		last := rows[len(rows)-1]
		after.AfterOccurredAt, after.AfterID = last.OccurredAt, last.ID
		logger.DebugContext(ctx, "rebuild progress", "events", res.Events, "at", last.OccurredAt)
	}

	if err := tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("commit rebuild: %w", err)
	}
	return res, nil
}

// eventOf decodes a stored row. It reports false for a kind this boopd
// does not know.
func eventOf(r *db.ListEventsAfterRow) (Event, bool, error) {
	kind := Kind(r.Kind)
	if _, ok := kinds[kind]; !ok {
		return Event{}, false, nil
	}
	data, err := DecodeData(kind, r.Data)
	if err != nil {
		return Event{}, false, fmt.Errorf("event %d (%s): %w", r.ID, r.Key, err)
	}
	return Event{
		Key:            r.Key,
		OccurredAt:     r.OccurredAt.In(time.UTC),
		Kind:           kind,
		Source:         Source(r.Source),
		InstallationID: deref(r.InstallationID),
		RepoID:         deref(r.RepoID),
		PRNumber:       int(deref(r.PrNumber)),
		Actor:          Actor{Login: deref(r.ActorLogin), Type: deref(r.ActorType)},
		CauseKey:       deref(r.CauseKey),
		Summary:        r.Summary,
		Data:           data,
	}, true, nil
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
