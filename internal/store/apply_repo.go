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
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/boop/internal/store/db"
)

// applyRepository applies the lifecycle kinds: onboarding sets
// onboarded_at and clears offboarded_at, offboarding sets offboarded_at,
// and a refresh changes only the names and profile.
func applyRepository(ctx context.Context, q *db.Queries, e *Event) error {
	d, err := payload[*Repository](e)
	if err != nil {
		return err
	}
	arg := db.UpsertRepositoryLifecycleParams{
		ID:             e.RepoID,
		InstallationID: e.InstallationID,
		Owner:          d.Owner,
		Slug:           d.Slug,
		Profile:        d.Profile,
		UpdatedAt:      e.OccurredAt,
	}
	at := e.OccurredAt
	switch e.Kind {
	case KindRepoOnboarded:
		arg.OnboardedAt = &at
	case KindRepoOffboarded:
		arg.OffboardedAt = &at
	default:
	}
	return q.UpsertRepositoryLifecycle(ctx, arg)
}

// applyRunScheduled sets next_due; a newer schedule wins.
func applyRunScheduled(ctx context.Context, q *db.Queries, e *Event) error {
	d, err := payload[*RunScheduled](e)
	if err != nil {
		return err
	}
	return q.ScheduleRepository(ctx, db.ScheduleRepositoryParams{NextDue: d.NextDue, ScheduledAt: e.OccurredAt, ID: e.RepoID})
}

// stateOf maps the state kinds to the repository state.
var stateOf = map[Kind]string{
	KindRunStalled:        RepoStateStalled,
	KindRunCapacityDenied: RepoStateWaiting,
	KindRunBudgetWait:     RepoStateRateLimited,
}

// applyRepoState sets the repository's state; a newer state wins.
func applyRepoState(ctx context.Context, q *db.Queries, e *Event) error {
	return setState(ctx, q, e.RepoID, stateOf[e.Kind], e.OccurredAt)
}

func setState(ctx context.Context, q *db.Queries, repoID int64, state string, at time.Time) error {
	return q.SetRepositoryState(ctx, db.SetRepositoryStateParams{State: state, StateAt: at, ID: repoID})
}

// applyRunStarted records the run and marks the repository running.
func applyRunStarted(ctx context.Context, q *db.Queries, e *Event) error {
	d, err := payload[*RunStarted](e)
	if err != nil {
		return err
	}
	if err := q.EnsureRepository(ctx, db.EnsureRepositoryParams{
		ID: e.RepoID, InstallationID: e.InstallationID, Owner: d.Owner, Slug: d.Slug,
	}); err != nil {
		return err
	}
	if err := q.InsertRunStarted(ctx, db.InsertRunStartedParams{
		ID:        d.RunID,
		RepoID:    e.RepoID,
		CauseKey:  nullText(e.CauseKey),
		StartedAt: e.OccurredAt,
		QueuedMs:  nullInt64(d.QueuedMS),
	}); err != nil {
		return err
	}
	return setState(ctx, q, e.RepoID, RepoStateRunning, e.OccurredAt)
}

// applyRunFinished records the run's result and the repository's state.
// When the run is the repository's newest and its report was read, the
// run's updates and dependencies replace the repository's.
func applyRunFinished(ctx context.Context, q *db.Queries, e *Event) error {
	d, err := payload[*RunFinished](e)
	if err != nil {
		return err
	}
	if err := q.EnsureRepository(ctx, db.EnsureRepositoryParams{
		ID: e.RepoID, InstallationID: e.InstallationID, Owner: d.Owner, Slug: d.Slug,
	}); err != nil {
		return err
	}
	if err := upsertRun(ctx, q, e, d); err != nil {
		return err
	}

	finished := orTime(d.FinishedAt, e.OccurredAt)
	_, err = q.RecordRepositoryRun(ctx, db.RecordRepositoryRunParams{
		RunID: d.RunID, FinishedAt: finished, Outcome: d.Outcome, ID: e.RepoID,
	})
	newest := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	state := RepoStateIdle
	if d.Outcome == RunFailed || d.Outcome == RunTimedOut {
		state = RepoStateErrored
	}
	if err := setState(ctx, q, e.RepoID, state, e.OccurredAt); err != nil {
		return err
	}
	if !newest || d.ReportMissing {
		return nil
	}
	return replaceInventory(ctx, q, e.RepoID, d)
}

func upsertRun(ctx context.Context, q *db.Queries, e *Event, d *RunFinished) error {
	finished := orTime(d.FinishedAt, e.OccurredAt)
	started := orTime(d.StartedAt, finished)
	spend, err := jsonValue(d.Spend, "{}")
	if err != nil {
		return err
	}
	problems, err := jsonValue(d.Problems, "[]")
	if err != nil {
		return err
	}
	return q.UpsertRunFinished(ctx, db.UpsertRunFinishedParams{
		ID:               d.RunID,
		RepoID:           e.RepoID,
		CauseKey:         nullText(e.CauseKey),
		Outcome:          d.Outcome,
		RepositoryResult: d.RepositoryResult,
		StartedAt:        started,
		FinishedAt:       finished,
		DurationMs:       finished.Sub(started).Milliseconds(),
		RenovateVersion:  d.RenovateVersion,
		Spend:            spend,
		Problems:         problems,
	})
}

// replaceInventory swaps the repository's branch updates and
// dependencies for the run's.
func replaceInventory(ctx context.Context, q *db.Queries, repoID int64, d *RunFinished) error {
	if err := q.DeleteBranchUpdates(ctx, repoID); err != nil {
		return err
	}
	if len(d.Updates) > 0 {
		args := make([]db.InsertBranchUpdateParams, 0, len(d.Updates))
		for i := range d.Updates {
			u := &d.Updates[i]
			args = append(args, db.InsertBranchUpdateParams{
				RepoID:      repoID,
				Branch:      u.Branch,
				PackageFile: u.PackageFile,
				PackageName: u.PackageName,
				RunID:       d.RunID,
				PrNumber:    nullInt32(u.PRNumber),
				Manager:     u.Manager,
				Datasource:  u.Datasource,
				DepName:     u.DepName,
				UpdateType:  u.UpdateType,
				FromVersion: u.FromVersion,
				ToVersion:   u.ToVersion,
			})
		}
		if err := batchErr(q.InsertBranchUpdate(ctx, args)); err != nil {
			return err
		}
	}
	if err := q.DeleteDependencies(ctx, repoID); err != nil {
		return err
	}
	if len(d.Dependencies) == 0 {
		return nil
	}
	args := make([]db.InsertDependencyParams, 0, len(d.Dependencies))
	for i := range d.Dependencies {
		dep := &d.Dependencies[i]
		args = append(args, db.InsertDependencyParams{
			RepoID:         repoID,
			Manager:        dep.Manager,
			PackageFile:    dep.PackageFile,
			PackageName:    dep.PackageName,
			RunID:          d.RunID,
			DepName:        dep.DepName,
			Datasource:     dep.Datasource,
			CurrentValue:   dep.CurrentValue,
			CurrentVersion: dep.CurrentVersion,
		})
	}
	return batchErr(q.InsertDependency(ctx, args))
}

// batchResults is a generated :batchexec result.
type batchResults interface {
	Exec(f func(int, error))
}

// batchErr runs a batch and returns its first error.
func batchErr(b batchResults) error {
	var first error
	b.Exec(func(_ int, err error) {
		if err != nil && first == nil {
			first = err
		}
	})
	return first
}

func orTime(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}
