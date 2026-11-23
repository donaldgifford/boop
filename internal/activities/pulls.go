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

package activities

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/platform"
	"github.com/donaldgifford/boop/internal/platform/github"
	"github.com/donaldgifford/boop/internal/renovate/dashboard"
	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/workflows"
)

// ReconcilePullRequests syncs the store's bot PRs and Dependency
// Dashboards on one installation with GitHub (DESIGN-0004 OQ5), for
// fleets or periods without webhooks:
//
//   - One GraphQL search lists the App's open PRs. Each PR the store
//     holds open that the search did not list is read once to learn
//     whether it merged or closed, and every one is appended as
//     pr.synced.
//   - One GraphQL search lists the App's open issues. Each whose body is
//     a dashboard is appended as dashboard.updated at GitHub's
//     updated_at. Renovate edits its dashboard only when it changes, so
//     without this a dashboard last edited before the store existed is
//     never seen.
//
// Each key carries GitHub's updated_at, so an unchanged PR or dashboard
// is a duplicate, and the projections' guards keep a newer webhook
// state. Its calls spend the installation's budget, which ReadRateLimit
// watches. With the store disabled, or the installation gone, it does
// nothing.
func (a *Activities) ReconcilePullRequests(ctx context.Context, in *workflows.ReconcilePRsInput) (*workflows.ReconcilePRsResult, error) {
	out := &workflows.ReconcilePRsResult{}
	if a.store == nil {
		return out, nil
	}
	app, err := a.app(in.App)
	if err != nil {
		return nil, err
	}
	api, err := a.gh.Installation(app, in.InstallationID, a.cfg.Renovate.ConfigPaths)
	if err != nil {
		return nil, err
	}
	gone, err := a.reconcilePRs(ctx, api, app, in, out)
	if err != nil {
		return nil, err
	}
	if gone {
		return out, nil
	}
	if err := a.reconcileDashboards(ctx, api, app, in, out); err != nil {
		return nil, err
	}
	return out, nil
}

// reconcilePRs appends the App's PRs on the installation as pr.synced.
// It reports true when the installation is gone.
func (a *Activities) reconcilePRs(
	ctx context.Context, api InstallationAPI, app *config.App, in *workflows.ReconcilePRsInput, out *workflows.ReconcilePRsResult,
) (bool, error) {
	search, err := api.SearchOpenBotPRs(ctx, appSlug(app), in.Account)
	if errors.Is(err, platform.ErrInstallationGone) {
		return true, nil
	}
	if err != nil {
		return false, honourRetryAfter(fmt.Errorf("search open pull requests of %d: %w", in.InstallationID, err))
	}
	out.Open, out.Cost = len(search.PRs), out.Cost+search.Cost
	known, err := a.store.OpenPullRequests(ctx, in.InstallationID)
	if err != nil {
		return false, err
	}
	listed := make(map[store.PRRef]bool, len(search.PRs))
	prs := search.PRs
	for i := range search.PRs {
		listed[store.PRRef{RepoID: search.PRs[i].RepoID, Number: search.PRs[i].Number}] = true
	}
	for _, ref := range known {
		if listed[ref] {
			continue
		}
		out.Left++
		activity.RecordHeartbeat(ctx, out.Left)
		pr, err := api.GetPullRequest(ctx, ref.RepoID, ref.Number)
		if errors.Is(err, platform.ErrNotFound) {
			// The repository or PR is gone; offboarding removes it.
			a.log.Info("pull request gone", "installation", in.InstallationID, "repo_id", ref.RepoID, "number", ref.Number)
			continue
		}
		if err != nil {
			return false, honourRetryAfter(fmt.Errorf("read pull request %d#%d: %w", ref.RepoID, ref.Number, err))
		}
		prs = append(prs, *pr)
	}
	if len(prs) > 0 {
		now := a.now()
		events := make([]store.Event, 0, len(prs))
		for i := range prs {
			events = append(events, syncedEvent(in.InstallationID, &prs[i], now))
		}
		if err := a.store.Append(ctx, events...); err != nil {
			return false, fmt.Errorf("record pull requests of %d: %w", in.InstallationID, err)
		}
		out.Synced = len(events)
	}
	return false, nil
}

// reconcileDashboards appends each of the App's open dashboards on the
// installation as dashboard.updated.
func (a *Activities) reconcileDashboards(
	ctx context.Context, api InstallationAPI, app *config.App, in *workflows.ReconcilePRsInput, out *workflows.ReconcilePRsResult,
) error {
	search, err := api.SearchOpenBotIssues(ctx, appSlug(app), in.Account)
	if errors.Is(err, platform.ErrInstallationGone) {
		return nil
	}
	if err != nil {
		return honourRetryAfter(fmt.Errorf("search open issues of %d: %w", in.InstallationID, err))
	}
	out.Cost += search.Cost
	var events []store.Event
	for i := range search.Issues {
		is := &search.Issues[i]
		snap, err := dashboard.Parse(is.Body)
		if err != nil {
			continue // not a dashboard
		}
		events = append(events, dashboardEvent(in.InstallationID, is, &snap))
	}
	if len(events) == 0 {
		return nil
	}
	if err := a.store.Append(ctx, events...); err != nil {
		return fmt.Errorf("record dashboards of %d: %w", in.InstallationID, err)
	}
	out.Dashboards = len(events)
	return nil
}

// dashboardRecordVersion is in the reconcile's dashboard key. Raise it
// when what a dashboard.updated records changes, so the next pass
// records every dashboard once more instead of skipping it as a
// duplicate of the same GitHub edit. 2: the issue URL, and no
// whole-repository boxes (0.14.0 recorded nothing new for an unchanged
// issue).
const dashboardRecordVersion = 2

// dashboardEvent is dashboard.updated for a dashboard as GitHub last
// described it, at GitHub's updated_at: the time ingest orders the same
// dashboard's webhooks by.
func dashboardEvent(installationID int64, is *github.Issue, snap *dashboard.Snapshot) store.Event {
	items := make([]store.DashboardItem, 0, len(snap.Items))
	for _, it := range snap.Items {
		items = append(items, store.DashboardItem{
			Marker: it.Raw, Branch: it.Branch, Section: dashboard.SectionKey(it.Section), Title: it.Title, Deps: it.Deps,
		})
	}
	return store.Event{
		Key: fmt.Sprintf("reconcile:dashboard:v%d:%d:%d:%s",
			dashboardRecordVersion, is.RepoID, is.Number, is.UpdatedAt.UTC().Format(time.RFC3339Nano)),
		OccurredAt:     is.UpdatedAt,
		Kind:           store.KindDashboardUpdated,
		Source:         store.SourceWorker,
		InstallationID: installationID,
		RepoID:         is.RepoID,
		Summary:        fmt.Sprintf("dashboard #%d synced: %d items", is.Number, len(items)),
		Data:           &store.DashboardUpdated{V: 1, Items: items, URL: is.URL},
	}
}

// appSlug is the App's slug: its bot login without "[bot]".
func appSlug(app *config.App) string {
	bot := app.Name + "[bot]"
	if app.Webhook != nil && app.Webhook.Bot != "" {
		bot = app.Webhook.Bot
	}
	return strings.TrimSuffix(bot, "[bot]")
}

// syncedEvent is pr.synced for pr as GitHub last described it.
func syncedEvent(installationID int64, pr *github.PullRequest, now time.Time) store.Event {
	opened := pr.CreatedAt
	return store.Event{
		Key:            fmt.Sprintf("reconcile:%d:%d:%s:%s", pr.RepoID, pr.Number, pr.UpdatedAt.UTC().Format(time.RFC3339Nano), pr.State),
		OccurredAt:     now,
		Kind:           store.KindPRSynced,
		Source:         store.SourceWorker,
		InstallationID: installationID,
		RepoID:         pr.RepoID,
		PRNumber:       pr.Number,
		Summary:        fmt.Sprintf("#%d synced: %s", pr.Number, pr.State),
		Data: &store.PullRequest{
			V: 1, Number: pr.Number, Branch: pr.Branch, State: pr.State, Title: pr.Title, Labels: pr.Labels,
			Automerge: pr.Automerge, URL: pr.URL, OpenedAt: &opened, MergedAt: pr.MergedAt, ClosedAt: pr.ClosedAt,
			UpdatedAt: pr.UpdatedAt,
		},
	}
}
