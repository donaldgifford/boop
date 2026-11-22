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

package api

import (
	"context"
	"net/http"
	"time"

	"github.com/donaldgifford/boop/internal/api/gen"
	"github.com/donaldgifford/boop/internal/store/db"
)

// The list endpoints (DESIGN-0004 § The API). Each reads limit+1 rows
// within the caller's scope, narrowed by an owner filter, and hashes its
// filters (less cursor and limit) into the next page's cursor.

// timelineLimit bounds a pull request's timeline.
const timelineLimit = 500

// scope is the caller's visible owners narrowed by an owner filter: an
// owner it cannot see is 404.
func scope(ctx context.Context, owner *string) (Scope, error) {
	p, err := principal(ctx)
	if err != nil {
		return Scope{}, err
	}
	return p.Visible.Narrow(owner)
}

// visibleRepo reads one repository within the caller's scope; unknown
// and invisible are both 404.
func (s *server) visibleRepo(ctx context.Context, id int64) (*db.ListRepositoriesRow, error) {
	sc, err := scope(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows, err := s.opts.Reader.Repositories(ctx, sc.Store(), &db.ListRepositoriesParams{ID: &id, Lim: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errNotFound
	}
	return &rows[0], nil
}

// Sort keys, as the cursor carries them.
type (
	idKey   struct{ ID int64 }
	slugKey struct {
		Slug string
		ID   int64
	}
	eventKey struct {
		At time.Time
		ID int64
	}
	runKey struct {
		At time.Time
		ID string
	}
	depKey struct {
		Repo                int64
		Manager, File, Name string
	}
	updateKey struct {
		Repo               int64
		Branch, File, Name string
	}
	packageKey struct {
		Repos int32
		Purl  string
	}
	prKey struct {
		At     time.Time
		Repo   int64
		Number int32
	}
	itemKey struct {
		At             time.Time
		Repo           int64
		Marker, Branch string
	}
	deliveryKey struct {
		At       time.Time
		Delivery string
		Attempt  int32
	}
)

// ListInstallations lists the visible installations.
func (s *server) ListInstallations(
	ctx context.Context, req gen.ListInstallationsRequestObject,
) (gen.ListInstallationsResponseObject, error) {
	sc, err := scope(ctx, nil)
	if err != nil {
		return nil, err
	}
	limit, err := limitOf(req.Params.Limit)
	if err != nil {
		return nil, err
	}
	f := req.Params
	f.Cursor, f.Limit = nil, nil
	q := db.ListInstallationsParams{Lim: limit + 1}
	var after idKey
	if ok, err := afterKey(req.Params.Cursor, &after, f); err != nil {
		return nil, err
	} else if ok {
		q.AfterID = &after.ID
	}
	rows, err := s.opts.Reader.Installations(ctx, sc.Store(), &q)
	if err != nil {
		return nil, err
	}
	rows, next, err := paged(rows, limit, func(r *db.ListInstallationsRow) idKey { return idKey{r.ID} }, f)
	if err != nil {
		return nil, err
	}
	items := make([]gen.Installation, 0, len(rows))
	for i := range rows {
		in, err := installation(&rows[i])
		if err != nil {
			return nil, err
		}
		items = append(items, in)
	}
	return gen.ListInstallations200JSONResponse{Items: items, NextCursor: next}, nil
}

// ListRepositories lists repositories by slug.
func (s *server) ListRepositories(
	ctx context.Context, req gen.ListRepositoriesRequestObject,
) (gen.ListRepositoriesResponseObject, error) {
	sc, err := scope(ctx, req.Params.Owner)
	if err != nil {
		return nil, err
	}
	limit, err := limitOf(req.Params.Limit)
	if err != nil {
		return nil, err
	}
	f := req.Params
	f.Cursor, f.Limit = nil, nil
	q := db.ListRepositoriesParams{
		SlugPrefix: slugPrefix(f.Q), State: nonEmpty(f.State), Profile: nonEmpty(f.Profile), HasOpenPrs: f.HasOpenPrs,
		Lim: limit + 1,
	}
	var after slugKey
	if ok, err := afterKey(req.Params.Cursor, &after, f); err != nil {
		return nil, err
	} else if ok {
		q.AfterSlug, q.AfterID = &after.Slug, &after.ID
	}
	rows, err := s.opts.Reader.Repositories(ctx, sc.Store(), &q)
	if err != nil {
		return nil, err
	}
	rows, next, err := paged(rows, limit, func(r *db.ListRepositoriesRow) slugKey { return slugKey{r.SortSlug, r.ID} }, f)
	if err != nil {
		return nil, err
	}
	items := make([]gen.Repository, 0, len(rows))
	for i := range rows {
		items = append(items, repository(&rows[i]))
	}
	return gen.ListRepositories200JSONResponse{Items: items, NextCursor: next}, nil
}

// repoFilters is a per-repository list's filters: the path id with the
// query.
type repoFilters[P any] struct {
	ID     int64
	Params P
}

// ListRepositoryEvents is one repository's timeline.
func (s *server) ListRepositoryEvents(
	ctx context.Context, req gen.ListRepositoryEventsRequestObject,
) (gen.ListRepositoryEventsResponseObject, error) {
	if _, err := s.visibleRepo(ctx, req.Id); err != nil {
		return nil, err
	}
	p := req.Params
	page, err := s.events(ctx, ScopeAll(), &eventQuery{
		repo: &req.Id, kind: p.Kind, since: p.Since, cursor: p.Cursor, limit: p.Limit,
	}, repoFilters[gen.ListRepositoryEventsParams]{ID: req.Id, Params: withoutPage(p, func(q *gen.ListRepositoryEventsParams) {
		q.Cursor, q.Limit = nil, nil
	})})
	if err != nil {
		return nil, err
	}
	return gen.ListRepositoryEvents200JSONResponse(*page), nil
}

// ListEvents is the audit log.
//
//nolint:gocritic // hugeParam: the generated strict interface passes requests by value
func (s *server) ListEvents(ctx context.Context, req gen.ListEventsRequestObject) (gen.ListEventsResponseObject, error) {
	p := req.Params
	sc, err := scope(ctx, p.Owner)
	if err != nil {
		return nil, err
	}
	page, err := s.events(ctx, sc, &eventQuery{
		repo: p.Repo, kind: p.Kind, source: p.Source, actor: p.Actor, causeKey: p.CauseKey,
		since: p.Since, until: p.Until, cursor: p.Cursor, limit: p.Limit,
	}, withoutPage(p, func(q *gen.ListEventsParams) { q.Cursor, q.Limit = nil, nil }))
	if err != nil {
		return nil, err
	}
	return gen.ListEvents200JSONResponse(*page), nil
}

// withoutPage is p with its paging fields cleared by drop, for the
// cursor's filter hash.
func withoutPage[P any](p P, drop func(*P)) P {
	drop(&p)
	return p
}

// eventQuery is ListEvents' filters.
type eventQuery struct {
	repo                          *int64
	kind, source, actor, causeKey *string
	since, until                  *time.Time
	cursor                        *string
	limit                         *int
}

func (s *server) events(ctx context.Context, sc Scope, e *eventQuery, filters any) (*gen.EventList, error) {
	limit, err := limitOf(e.limit)
	if err != nil {
		return nil, err
	}
	q := db.ListEventsParams{
		RepoID: e.repo, Kind: nonEmpty(e.kind), Source: nonEmpty(e.source), Actor: nonEmpty(e.actor),
		CauseKey: nonEmpty(e.causeKey), Since: e.since, Until: e.until, Lim: limit + 1,
	}
	var after eventKey
	if ok, err := afterKey(e.cursor, &after, filters); err != nil {
		return nil, err
	} else if ok {
		q.AfterAt, q.AfterID = &after.At, &after.ID
	}
	rows, err := s.opts.Reader.Events(ctx, sc.Store(), &q)
	if err != nil {
		return nil, err
	}
	rows, next, err := paged(rows, limit, func(r *db.Event) eventKey { return eventKey{r.OccurredAt, r.ID} }, filters)
	if err != nil {
		return nil, err
	}
	items, err := events(rows)
	if err != nil {
		return nil, err
	}
	return &gen.EventList{Items: items, NextCursor: next}, nil
}

// ListRepositoryRuns is one repository's runs.
func (s *server) ListRepositoryRuns(
	ctx context.Context, req gen.ListRepositoryRunsRequestObject,
) (gen.ListRepositoryRunsResponseObject, error) {
	if _, err := s.visibleRepo(ctx, req.Id); err != nil {
		return nil, err
	}
	p := req.Params
	page, err := s.runs(ctx, ScopeAll(), &runQuery{repo: &req.Id, cursor: p.Cursor, limit: p.Limit},
		repoFilters[gen.ListRepositoryRunsParams]{ID: req.Id})
	if err != nil {
		return nil, err
	}
	return gen.ListRepositoryRuns200JSONResponse(*page), nil
}

// ListRuns lists runs across the visible repositories.
func (s *server) ListRuns(ctx context.Context, req gen.ListRunsRequestObject) (gen.ListRunsResponseObject, error) {
	p := req.Params
	sc, err := scope(ctx, p.Owner)
	if err != nil {
		return nil, err
	}
	page, err := s.runs(ctx, sc, &runQuery{
		outcome: p.Outcome, cause: p.Cause, since: p.Since, until: p.Until, cursor: p.Cursor, limit: p.Limit,
	}, withoutPage(p, func(q *gen.ListRunsParams) { q.Cursor, q.Limit = nil, nil }))
	if err != nil {
		return nil, err
	}
	return gen.ListRuns200JSONResponse(*page), nil
}

// runQuery is ListRuns' filters.
type runQuery struct {
	repo           *int64
	outcome, cause *string
	since, until   *time.Time
	cursor         *string
	limit          *int
}

func (s *server) runs(ctx context.Context, sc Scope, r *runQuery, filters any) (*gen.RunList, error) {
	limit, err := limitOf(r.limit)
	if err != nil {
		return nil, err
	}
	q := db.ListRunsParams{
		RepoID: r.repo, Outcome: nonEmpty(r.outcome), Cause: nonEmpty(r.cause), Since: r.since, Until: r.until,
		Lim: limit + 1,
	}
	var after runKey
	if ok, err := afterKey(r.cursor, &after, filters); err != nil {
		return nil, err
	} else if ok {
		q.AfterAt, q.AfterID = &after.At, &after.ID
	}
	rows, err := s.opts.Reader.Runs(ctx, sc.Store(), &q)
	if err != nil {
		return nil, err
	}
	rows, next, err := paged(rows, limit, func(r *db.ListRunsRow) runKey { return runKey{r.StartedAt, r.ID} }, filters)
	if err != nil {
		return nil, err
	}
	items := make([]gen.Run, 0, len(rows))
	for i := range rows {
		ru, err := run(&rows[i])
		if err != nil {
			return nil, err
		}
		items = append(items, ru)
	}
	return &gen.RunList{Items: items, NextCursor: next}, nil
}

// ListRepositoryDependencies is one repository's dependencies.
func (s *server) ListRepositoryDependencies(
	ctx context.Context, req gen.ListRepositoryDependenciesRequestObject,
) (gen.ListRepositoryDependenciesResponseObject, error) {
	if _, err := s.visibleRepo(ctx, req.Id); err != nil {
		return nil, err
	}
	p := req.Params
	page, err := s.dependencies(ctx, ScopeAll(), &db.ListDependenciesParams{RepoID: &req.Id}, p.Cursor, p.Limit,
		repoFilters[gen.ListRepositoryDependenciesParams]{ID: req.Id})
	if err != nil {
		return nil, err
	}
	return gen.ListRepositoryDependencies200JSONResponse(*page), nil
}

// ListDependencies lists dependencies across the visible repositories.
func (s *server) ListDependencies(
	ctx context.Context, req gen.ListDependenciesRequestObject,
) (gen.ListDependenciesResponseObject, error) {
	p := req.Params
	sc, err := scope(ctx, p.Owner)
	if err != nil {
		return nil, err
	}
	m := purlFilter(p.Purl)
	page, err := s.dependencies(ctx, sc, &db.ListDependenciesParams{
		Manager: nonEmpty(p.Manager), PurlExact: m.exact, PurlBase: m.base, PurlPrefix: m.prefix,
	}, p.Cursor, p.Limit, withoutPage(p, func(q *gen.ListDependenciesParams) { q.Cursor, q.Limit = nil, nil }))
	if err != nil {
		return nil, err
	}
	return gen.ListDependencies200JSONResponse(*page), nil
}

func (s *server) dependencies(
	ctx context.Context, sc Scope, q *db.ListDependenciesParams, cursor *string, l *int, filters any,
) (*gen.DependencyList, error) {
	limit, err := limitOf(l)
	if err != nil {
		return nil, err
	}
	q.Lim = limit + 1
	var after depKey
	if ok, err := afterKey(cursor, &after, filters); err != nil {
		return nil, err
	} else if ok {
		q.AfterRepo, q.AfterManager, q.AfterFile, q.AfterName = &after.Repo, &after.Manager, &after.File, &after.Name
	}
	rows, err := s.opts.Reader.Dependencies(ctx, sc.Store(), q)
	if err != nil {
		return nil, err
	}
	rows, next, err := paged(rows, limit, func(r *db.ListDependenciesRow) depKey {
		return depKey{r.RepoID, r.Manager, r.PackageFile, r.PackageName}
	}, filters)
	if err != nil {
		return nil, err
	}
	items := make([]gen.Dependency, 0, len(rows))
	for i := range rows {
		items = append(items, dependency(&rows[i]))
	}
	return &gen.DependencyList{Items: items, NextCursor: next}, nil
}

// ListUpdates lists the proposed updates.
func (s *server) ListUpdates(ctx context.Context, req gen.ListUpdatesRequestObject) (gen.ListUpdatesResponseObject, error) {
	p := req.Params
	sc, err := scope(ctx, p.Owner)
	if err != nil {
		return nil, err
	}
	limit, err := limitOf(p.Limit)
	if err != nil {
		return nil, err
	}
	f := withoutPage(p, func(q *gen.ListUpdatesParams) { q.Cursor, q.Limit = nil, nil })
	m := purlFilter(p.Purl)
	q := db.ListUpdatesParams{
		PurlExact: m.exact, PurlBase: m.base, PurlPrefix: m.prefix, FromVersion: nonEmpty(p.From),
		ToVersion: nonEmpty(p.To), UpdateType: nonEmpty(p.UpdateType), Lim: limit + 1,
	}
	var after updateKey
	if ok, err := afterKey(p.Cursor, &after, f); err != nil {
		return nil, err
	} else if ok {
		q.AfterRepo, q.AfterBranch, q.AfterFile, q.AfterName = &after.Repo, &after.Branch, &after.File, &after.Name
	}
	rows, err := s.opts.Reader.Updates(ctx, sc.Store(), &q)
	if err != nil {
		return nil, err
	}
	rows, next, err := paged(rows, limit, func(r *db.ListUpdatesRow) updateKey {
		return updateKey{r.RepoID, r.Branch, r.PackageFile, r.PackageName}
	}, f)
	if err != nil {
		return nil, err
	}
	items := make([]gen.Update, 0, len(rows))
	for i := range rows {
		items = append(items, update(&rows[i]))
	}
	return gen.ListUpdates200JSONResponse{Items: items, NextCursor: next}, nil
}

// ListPackages groups pending updates by package.
func (s *server) ListPackages(ctx context.Context, req gen.ListPackagesRequestObject) (gen.ListPackagesResponseObject, error) {
	p := req.Params
	sc, err := scope(ctx, p.Owner)
	if err != nil {
		return nil, err
	}
	limit, err := limitOf(p.Limit)
	if err != nil {
		return nil, err
	}
	f := withoutPage(p, func(q *gen.ListPackagesParams) { q.Cursor, q.Limit = nil, nil })
	q := db.ListPackagesParams{Manager: nonEmpty(p.Manager), Lim: limit + 1}
	var after packageKey
	if ok, err := afterKey(p.Cursor, &after, f); err != nil {
		return nil, err
	} else if ok {
		q.AfterRepos, q.AfterPurl = &after.Repos, &after.Purl
	}
	rows, err := s.opts.Reader.Packages(ctx, sc.Store(), &q)
	if err != nil {
		return nil, err
	}
	rows, next, err := paged(rows, limit, func(r *db.ListPackagesRow) packageKey { return packageKey{r.Repositories, r.Purl} }, f)
	if err != nil {
		return nil, err
	}
	items := make([]gen.Package, 0, len(rows))
	for i := range rows {
		items = append(items, pkg(&rows[i]))
	}
	return gen.ListPackages200JSONResponse{Items: items, NextCursor: next}, nil
}

// ListPullRequests lists Renovate's pull requests with their updates.
//
//nolint:gocritic // hugeParam: the generated strict interface passes requests by value
func (s *server) ListPullRequests(
	ctx context.Context, req gen.ListPullRequestsRequestObject,
) (gen.ListPullRequestsResponseObject, error) {
	p := req.Params
	sc, err := scope(ctx, p.Owner)
	if err != nil {
		return nil, err
	}
	limit, err := limitOf(p.Limit)
	if err != nil {
		return nil, err
	}
	f := withoutPage(p, func(q *gen.ListPullRequestsParams) { q.Cursor, q.Limit = nil, nil })
	m := purlFilter(p.Purl)
	q := db.ListPullRequestsParams{
		RepoID: p.Repo, State: nonEmpty(p.State), Automerge: p.Automerge, Label: nonEmpty(p.Label),
		Manager: nonEmpty(p.Manager), UpdateType: nonEmpty(p.UpdateType),
		PurlExact: m.exact, PurlBase: m.base, PurlPrefix: m.prefix, Lim: limit + 1,
	}
	if p.OlderThan != nil {
		before := s.opts.Now().Add(-time.Duration(*p.OlderThan) * 24 * time.Hour)
		q.OpenedBefore = &before
	}
	var after prKey
	if ok, err := afterKey(p.Cursor, &after, f); err != nil {
		return nil, err
	} else if ok {
		q.AfterAt, q.AfterRepo, q.AfterNumber = &after.At, &after.Repo, &after.Number
	}
	rows, err := s.opts.Reader.PullRequests(ctx, sc.Store(), &q)
	if err != nil {
		return nil, err
	}
	rows, next, err := paged(rows, limit, func(r *db.ListPullRequestsRow) prKey { return prKey{r.SortAt, r.RepoID, r.Number} }, f)
	if err != nil {
		return nil, err
	}
	items, err := s.withUpdates(ctx, rows)
	if err != nil {
		return nil, err
	}
	return gen.ListPullRequests200JSONResponse{Items: items, NextCursor: next}, nil
}

// withUpdates converts pull requests, attaching each one's branch
// updates from one read.
func (s *server) withUpdates(ctx context.Context, rows []db.ListPullRequestsRow) ([]gen.PullRequest, error) {
	type branchOf struct {
		repo   int64
		branch string
	}
	ids, branches := make([]int64, 0, len(rows)), make([]string, 0, len(rows))
	for i := range rows {
		ids, branches = append(ids, rows[i].RepoID), append(branches, rows[i].Branch)
	}
	updates := make(map[branchOf][]gen.Update)
	if len(rows) > 0 {
		ups, err := s.opts.Reader.BranchUpdates(ctx, ids, branches)
		if err != nil {
			return nil, err
		}
		for i := range ups {
			k := branchOf{ups[i].RepoID, ups[i].Branch}
			updates[k] = append(updates[k], update(&ups[i]))
		}
	}
	out := make([]gen.PullRequest, 0, len(rows))
	for i := range rows {
		out = append(out, pullRequest(&rows[i], updates[branchOf{rows[i].RepoID, rows[i].Branch}]))
	}
	return out, nil
}

// GetPullRequest is one pull request with its updates and timeline.
func (s *server) GetPullRequest(ctx context.Context, req gen.GetPullRequestRequestObject) (gen.GetPullRequestResponseObject, error) {
	sc, err := scope(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows, err := s.opts.Reader.PullRequests(ctx, sc.Store(), &db.ListPullRequestsParams{
		RepoID: &req.RepoId, Number: &req.Number, Lim: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errNotFound
	}
	prs, err := s.withUpdates(ctx, rows)
	if err != nil {
		return nil, err
	}
	timeline, err := s.opts.Reader.PullRequestTimeline(ctx, req.RepoId, req.Number, timelineLimit)
	if err != nil {
		return nil, err
	}
	evs, err := events(timeline)
	if err != nil {
		return nil, err
	}
	pr := prs[0]
	return gen.GetPullRequest200JSONResponse{
		RepoId: pr.RepoId, Owner: pr.Owner, Slug: pr.Slug, Number: pr.Number, Branch: pr.Branch, State: pr.State,
		Title: pr.Title, Labels: pr.Labels, Automerge: pr.Automerge, Url: pr.Url, OpenedAt: pr.OpenedAt,
		MergedAt: pr.MergedAt, ClosedAt: pr.ClosedAt, UpdatedAt: pr.UpdatedAt, Updates: pr.Updates, Timeline: evs,
	}, nil
}

// ListDashboardItems lists Dependency Dashboard items.
func (s *server) ListDashboardItems(
	ctx context.Context, req gen.ListDashboardItemsRequestObject,
) (gen.ListDashboardItemsResponseObject, error) {
	p := req.Params
	sc, err := scope(ctx, p.Owner)
	if err != nil {
		return nil, err
	}
	limit, err := limitOf(p.Limit)
	if err != nil {
		return nil, err
	}
	f := withoutPage(p, func(q *gen.ListDashboardItemsParams) { q.Cursor, q.Limit = nil, nil })
	q := db.ListDashboardItemsParams{RepoID: p.Repo, Section: nonEmpty(p.Section), Lim: limit + 1}
	if p.WaitingLongerThan != nil {
		before := s.opts.Now().Add(-time.Duration(*p.WaitingLongerThan) * 24 * time.Hour)
		q.SeenBefore = &before
	}
	var after itemKey
	if ok, err := afterKey(p.Cursor, &after, f); err != nil {
		return nil, err
	} else if ok {
		q.AfterAt, q.AfterRepo, q.AfterMarker, q.AfterBranch = &after.At, &after.Repo, &after.Marker, &after.Branch
	}
	rows, err := s.opts.Reader.DashboardItems(ctx, sc.Store(), &q)
	if err != nil {
		return nil, err
	}
	rows, next, err := paged(rows, limit, func(r *db.ListDashboardItemsRow) itemKey {
		return itemKey{r.FirstSeenAt, r.RepoID, r.Marker, r.Branch}
	}, f)
	if err != nil {
		return nil, err
	}
	items := make([]gen.DashboardItem, 0, len(rows))
	for i := range rows {
		it, err := dashboardItem(&rows[i])
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return gen.ListDashboardItems200JSONResponse{Items: items, NextCursor: next}, nil
}

// ListDeliveries lists webhook deliveries; it needs an every-owner view.
func (s *server) ListDeliveries(ctx context.Context, req gen.ListDeliveriesRequestObject) (gen.ListDeliveriesResponseObject, error) {
	pr, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if !pr.Visible.All() {
		return nil, statusError(http.StatusForbidden, "deliveries need an every-owner view")
	}
	p := req.Params
	limit, err := limitOf(p.Limit)
	if err != nil {
		return nil, err
	}
	f := withoutPage(p, func(q *gen.ListDeliveriesParams) { q.Cursor, q.Limit = nil, nil })
	q := db.ListDeliveriesParams{Event: nonEmpty(p.Event), Outcome: nonEmpty(p.Outcome), Missed: p.Missed, Lim: limit + 1}
	if p.GithubStatus != nil {
		if *p.GithubStatus < 0 || *p.GithubStatus > 599 {
			return nil, statusError(http.StatusBadRequest, "github_status must be 0 to 599")
		}
		st := int32(*p.GithubStatus)
		q.GithubStatus = &st
	}
	var after deliveryKey
	if ok, err := afterKey(p.Cursor, &after, f); err != nil {
		return nil, err
	} else if ok {
		q.AfterAt, q.AfterDelivery, q.AfterAttempt = &after.At, &after.Delivery, &after.Attempt
	}
	rows, err := s.opts.Reader.Deliveries(ctx, &q)
	if err != nil {
		return nil, err
	}
	rows, next, err := paged(rows, limit, func(r *db.ListDeliveriesRow) deliveryKey {
		return deliveryKey{r.SortAt, r.DeliveryID, r.Attempt}
	}, f)
	if err != nil {
		return nil, err
	}
	items := make([]gen.Delivery, 0, len(rows))
	for i := range rows {
		d, err := delivery(&rows[i])
		if err != nil {
			return nil, err
		}
		items = append(items, d)
	}
	return gen.ListDeliveries200JSONResponse{Items: items, NextCursor: next}, nil
}
