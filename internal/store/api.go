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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/donaldgifford/boop/internal/store/db"
)

// ErrNotFound is a read of one resource that is unknown or outside the
// caller's scope; the API answers both 404.
var ErrNotFound = errors.New("not found")

// Ping checks the store answers, for the API role's readiness.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping store: %w", err)
	}
	return nil
}

// SchemaReady fails when the store's schema is older than
// SchemaVersion, for the API role's readiness.
func (s *Store) SchemaReady(ctx context.Context) error {
	return RequireVersion(ctx, s.pool, SchemaVersion)
}

// owners is the scope's owners as the @owners array: never NULL.
func (sc Scope) owners() []string {
	if sc.Owners == nil {
		return []string{}
	}
	return sc.Owners
}

// Owners is the installation accounts in scope the store knows,
// lowercased and sorted: /me's owner list.
func (s *Store) Owners(ctx context.Context, scope Scope) ([]string, error) {
	owners, err := db.New(s.pool).VisibleOwners(ctx, db.VisibleOwnersParams{AllOwners: scope.All, Owners: scope.owners()})
	if err != nil {
		return nil, fmt.Errorf("read owners: %w", err)
	}
	return owners, nil
}

// Installations runs ListInstallations within scope, which replaces p's own.
func (s *Store) Installations(ctx context.Context, scope Scope, p *db.ListInstallationsParams) ([]db.ListInstallationsRow, error) {
	q := *p
	q.AllOwners, q.Owners = scope.All, scope.owners()
	rows, err := db.New(s.pool).ListInstallations(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list installations: %w", err)
	}
	return rows, nil
}

// Repositories runs ListRepositories within scope, which replaces p's own.
func (s *Store) Repositories(ctx context.Context, scope Scope, p *db.ListRepositoriesParams) ([]db.ListRepositoriesRow, error) {
	q := *p
	q.AllOwners, q.Owners = scope.All, scope.owners()
	rows, err := db.New(s.pool).ListRepositories(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list repositories: %w", err)
	}
	return rows, nil
}

// Events runs ListEvents within scope, which replaces p's own.
func (s *Store) Events(ctx context.Context, scope Scope, p *db.ListEventsParams) ([]db.Event, error) {
	q := *p
	q.AllOwners, q.Owners = scope.All, scope.owners()
	rows, err := db.New(s.pool).ListEvents(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return rows, nil
}

// Runs runs ListRuns within scope, which replaces p's own.
func (s *Store) Runs(ctx context.Context, scope Scope, p *db.ListRunsParams) ([]db.ListRunsRow, error) {
	q := *p
	q.AllOwners, q.Owners = scope.All, scope.owners()
	rows, err := db.New(s.pool).ListRuns(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	return rows, nil
}

// Dependencies runs ListDependencies within scope, which replaces p's own.
func (s *Store) Dependencies(ctx context.Context, scope Scope, p *db.ListDependenciesParams) ([]db.ListDependenciesRow, error) {
	q := *p
	q.AllOwners, q.Owners = scope.All, scope.owners()
	rows, err := db.New(s.pool).ListDependencies(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list dependencies: %w", err)
	}
	return rows, nil
}

// Updates runs ListUpdates within scope, which replaces p's own.
func (s *Store) Updates(ctx context.Context, scope Scope, p *db.ListUpdatesParams) ([]db.ListUpdatesRow, error) {
	q := *p
	q.AllOwners, q.Owners = scope.All, scope.owners()
	rows, err := db.New(s.pool).ListUpdates(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list updates: %w", err)
	}
	return rows, nil
}

// Packages runs ListPackages within scope, which replaces p's own.
func (s *Store) Packages(ctx context.Context, scope Scope, p *db.ListPackagesParams) ([]db.ListPackagesRow, error) {
	q := *p
	q.AllOwners, q.Owners = scope.All, scope.owners()
	rows, err := db.New(s.pool).ListPackages(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list packages: %w", err)
	}
	return rows, nil
}

// PullRequests runs ListPullRequests within scope, which replaces p's own.
func (s *Store) PullRequests(ctx context.Context, scope Scope, p *db.ListPullRequestsParams) ([]db.ListPullRequestsRow, error) {
	q := *p
	q.AllOwners, q.Owners = scope.All, scope.owners()
	rows, err := db.New(s.pool).ListPullRequests(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list pull requests: %w", err)
	}
	return rows, nil
}

// DashboardItems runs ListDashboardItems within scope, which replaces p's own.
func (s *Store) DashboardItems(ctx context.Context, scope Scope, p *db.ListDashboardItemsParams) ([]db.ListDashboardItemsRow, error) {
	q := *p
	q.AllOwners, q.Owners = scope.All, scope.owners()
	rows, err := db.New(s.pool).ListDashboardItems(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list dashboard items: %w", err)
	}
	return rows, nil
}

// BranchUpdates is the updates on the given (repository, branch) pairs,
// which the caller took from a scoped read.
func (s *Store) BranchUpdates(ctx context.Context, repoIDs []int64, branches []string) ([]db.BranchUpdatesRow, error) {
	rows, err := db.New(s.pool).BranchUpdates(ctx, db.BranchUpdatesParams{RepoIds: repoIDs, Branches: branches})
	if err != nil {
		return nil, fmt.Errorf("read branch updates: %w", err)
	}
	return rows, nil
}

// PullRequestTimeline is one pull request's events, oldest first, at
// most limit; the caller has read the pull request within scope.
func (s *Store) PullRequestTimeline(ctx context.Context, repoID int64, number, limit int32) ([]db.Event, error) {
	rows, err := db.New(s.pool).PullRequestTimeline(ctx, db.PullRequestTimelineParams{RepoID: &repoID, Number: &number, Lim: limit})
	if err != nil {
		return nil, fmt.Errorf("read pull request timeline: %w", err)
	}
	return rows, nil
}

// Deliveries runs ListDeliveries. It is unscoped: deliveries belong to
// installations, so the API serves them to an every-owner view only.
func (s *Store) Deliveries(ctx context.Context, p *db.ListDeliveriesParams) ([]db.ListDeliveriesRow, error) {
	rows, err := db.New(s.pool).ListDeliveries(ctx, *p)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	return rows, nil
}

// Estate is the estate's numbers for a scope, read in one snapshot.
type Estate struct {
	Numbers     db.EstateNumbersRow
	OldestOpen  *time.Time
	Ages        db.EstateAgeBucketsRow
	Ecosystems  []db.EstateEcosystemsRow
	TopPackages []db.ListPackagesRow
	// Timings is each query's duration, for the rollup decision.
	Timings map[string]time.Duration
}

// TopPackages is how many packages Estate lists.
const TopPackages = 10

// Estate reads the estate's numbers at now over rangeStart..now in one
// read-only repeatable-read transaction, so they agree with each other.
func (s *Store) Estate(ctx context.Context, scope Scope, now, rangeStart time.Time) (*Estate, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("estate: begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx) //nolint:errcheck // read-only: nothing to undo, and the read's error is the one to report
	}()
	q := db.New(tx)
	out := &Estate{Timings: make(map[string]time.Duration)}
	timed := func(name string, f func() error) error {
		start := time.Now()
		err := f()
		out.Timings[name] = time.Since(start)
		if err != nil {
			return fmt.Errorf("estate %s: %w", name, err)
		}
		return nil
	}
	all, owners := scope.All, scope.owners()
	day := now.Add(-24 * time.Hour)
	if err := timed("numbers", func() (err error) {
		out.Numbers, err = q.EstateNumbers(ctx, db.EstateNumbersParams{AllOwners: all, Owners: owners, DayStart: day, RangeStart: rangeStart})
		return err
	}); err != nil {
		return nil, err
	}
	out.OldestOpen = timeOrNil(out.Numbers.OldestOpen)
	if err := timed("ages", func() (err error) {
		out.Ages, err = q.EstateAgeBuckets(ctx, db.EstateAgeBucketsParams{
			D1: now.Add(-24 * time.Hour), D7: now.Add(-7 * 24 * time.Hour), D30: now.Add(-30 * 24 * time.Hour),
			D90: now.Add(-90 * 24 * time.Hour), AllOwners: all, Owners: owners,
		})
		return err
	}); err != nil {
		return nil, err
	}
	if err := timed("ecosystems", func() (err error) {
		out.Ecosystems, err = q.EstateEcosystems(ctx, db.EstateEcosystemsParams{AllOwners: all, Owners: owners})
		return err
	}); err != nil {
		return nil, err
	}
	if err := timed("packages", func() (err error) {
		out.TopPackages, err = q.ListPackages(ctx, db.ListPackagesParams{AllOwners: all, Owners: owners, Lim: TopPackages})
		return err
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// Series metrics (the estate series' metric parameter).
const (
	SeriesOpenPRs = "open_prs"
	SeriesRuns    = "runs"
	SeriesWaiting = "waiting"
	SeriesMerged  = "merged"
)

// ErrUnknownMetric is a Series metric outside the Series* names.
var ErrUnknownMetric = errors.New("unknown series metric")

// SeriesPoint is one bucket: its start, value and, for runs, the count
// per outcome.
type SeriesPoint struct {
	At    time.Time
	Value int
	By    map[string]int
}

// seriesRow is one bucket's count, with its outcome for runs.
type seriesRow struct {
	at      time.Time
	outcome string
	count   int32
}

// seriesQueries reads each metric's rows.
var seriesQueries = map[string]func(ctx context.Context, q *db.Queries, a *seriesArgs) ([]seriesRow, error){
	SeriesRuns: func(ctx context.Context, q *db.Queries, a *seriesArgs) ([]seriesRow, error) {
		rows, err := q.SeriesRuns(ctx, db.SeriesRunsParams{Step: a.step, Origin: a.origin, AllOwners: a.all, Owners: a.owners})
		out := make([]seriesRow, 0, len(rows))
		for _, r := range rows {
			out = append(out, seriesRow{r.At, r.Outcome, r.Count})
		}
		return out, err
	},
	SeriesMerged: func(ctx context.Context, q *db.Queries, a *seriesArgs) ([]seriesRow, error) {
		rows, err := q.SeriesMerged(ctx, db.SeriesMergedParams{Step: a.step, Origin: a.origin, AllOwners: a.all, Owners: a.owners})
		out := make([]seriesRow, 0, len(rows))
		for _, r := range rows {
			out = append(out, seriesRow{at: r.At, count: r.Count})
		}
		return out, err
	},
	SeriesOpenPRs: func(ctx context.Context, q *db.Queries, a *seriesArgs) ([]seriesRow, error) {
		rows, err := q.SeriesOpenPRs(ctx, db.SeriesOpenPRsParams{
			Origin: a.origin, Last: a.last, Step: a.step, AllOwners: a.all, Owners: a.owners,
		})
		out := make([]seriesRow, 0, len(rows))
		for _, r := range rows {
			out = append(out, seriesRow{at: r.At, count: r.Count})
		}
		return out, err
	},
	SeriesWaiting: func(ctx context.Context, q *db.Queries, a *seriesArgs) ([]seriesRow, error) {
		rows, err := q.SeriesWaiting(ctx, db.SeriesWaitingParams{
			Origin: a.origin, Last: a.last, Step: a.step, AllOwners: a.all, Owners: a.owners,
		})
		out := make([]seriesRow, 0, len(rows))
		for _, r := range rows {
			out = append(out, seriesRow{at: r.At, count: r.Count})
		}
		return out, err
	},
}

type seriesArgs struct {
	step         pgtype.Interval
	origin, last time.Time
	all          bool
	owners       []string
}

// Series reads metric in buckets of step from origin, the first
// bucket's start, through last, the last bucket's start. Every bucket is
// present, zero when empty.
func (s *Store) Series(ctx context.Context, scope Scope, metric string, origin, last time.Time, step time.Duration) ([]SeriesPoint, error) {
	read, ok := seriesQueries[metric]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownMetric, metric)
	}
	rows, err := read(ctx, db.New(s.pool), &seriesArgs{
		step: pgtypeInterval(step), origin: origin, last: last, all: scope.All, owners: scope.owners(),
	})
	if err != nil {
		return nil, fmt.Errorf("series %s: %w", metric, err)
	}
	points := make(map[time.Time]*SeriesPoint)
	var out []SeriesPoint
	for at := origin.UTC(); !at.After(last); at = at.Add(step) {
		out = append(out, SeriesPoint{At: at})
	}
	for i := range out {
		points[out[i].At] = &out[i]
	}
	for _, r := range rows {
		p, ok := points[r.at.UTC()]
		if !ok {
			continue
		}
		p.Value += int(r.count)
		if r.outcome != "" {
			if p.By == nil {
				p.By = make(map[string]int)
			}
			p.By[r.outcome] += int(r.count)
		}
	}
	return out, nil
}

// StatusNumbers is the status page's aggregates; unscoped, since the page
// is aggregate-only.
type StatusNumbers struct {
	db.StatusNumbersRow
	LastDelivery  *time.Time
	LastDiscovery *time.Time
	// Spends is each installation's latest recorded spend.
	Spends []db.LatestSpendsRow
}

// Status reads the status page's aggregates at now.
func (s *Store) Status(ctx context.Context, now time.Time) (*StatusNumbers, error) {
	q := db.New(s.pool)
	row, err := q.StatusNumbers(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("status numbers: %w", err)
	}
	spends, err := q.LatestSpends(ctx)
	if err != nil {
		return nil, fmt.Errorf("status spends: %w", err)
	}
	return &StatusNumbers{
		StatusNumbersRow: row, LastDelivery: timeOrNil(row.LastDelivery), LastDiscovery: timeOrNil(row.LastDiscovery),
		Spends: spends,
	}, nil
}

// timeOrNil unwraps a nullable timestamptz sqlc could not type.
func timeOrNil(v any) *time.Time {
	t, ok := v.(time.Time)
	if !ok {
		return nil
	}
	return &t
}

// pgtypeInterval is d as a Postgres interval.
func pgtypeInterval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}
