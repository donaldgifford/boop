//go:build integration

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

package activities_test

import (
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/storetest"
	"github.com/donaldgifford/boop/internal/workflows"
	"github.com/donaldgifford/boop/test/fakegithub"
)

// TestReconcilePullRequests_MatchesGitHub runs the reconcile with ingest
// off: a PR opened, one merged and one closed since the store last heard
// are synced, and a newer webhook state survives a stale search answer.
// A second pass appends nothing new.
func TestReconcilePullRequests_MatchesGitHub(t *testing.T) {
	srv := storetest.Start(t)
	if _, err := store.Migrate(t.Context(), srv.OwnerDSN(), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	owner, err := store.Open(t.Context(), srv.OwnerDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	worker, err := store.Open(t.Context(), srv.RoleDSN(storetest.RoleWorker))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Close)
	ingest, err := store.Open(t.Context(), srv.RoleDSN(storetest.RoleIngest))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ingest.Close)

	t1 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	if _, err := owner.Exec(t.Context(),
		"INSERT INTO repositories (id, installation_id, owner, slug, updated_at) VALUES (1, 1, 'acme', 'acme/api', $1)", t1); err != nil {
		t.Fatal(err)
	}
	// What ingest last heard: #2 and #3 open, #5 closed at t2.
	webhook := func(number int, state string, at time.Time) store.Event {
		return store.Event{
			Key: fmt.Sprintf("seed:%d:%s", number, state), OccurredAt: at, Kind: store.KindPROpened,
			Source: store.SourceWebhook, InstallationID: 1, RepoID: 1, PRNumber: number,
			Data: &store.PullRequest{V: 1, Number: number, State: state, Branch: "renovate/x", OpenedAt: &t1, UpdatedAt: at},
		}
	}
	if err := store.New(ingest, storetest.RoleIngest, nil).Append(t.Context(),
		webhook(2, store.PRStateOpen, t1), webhook(3, store.PRStateOpen, t1), webhook(5, store.PRStateClosed, t2)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	gh := fakegithub.New(t)
	gh.SetRepos(fakegithub.Repo{ID: 1, NodeID: "R_1", Slug: "acme/api", DefaultBranch: "main"})
	const bot = "boop-bot[bot]"
	gh.SetPullRequests(
		fakegithub.PR{RepoID: 1, Number: 1, Author: bot, State: fakegithub.PROpen, Title: "new", Branch: "renovate/a", CreatedAt: t2, UpdatedAt: t2},
		fakegithub.PR{
			RepoID:    1,
			Number:    2,
			Author:    bot,
			State:     fakegithub.PRMerged,
			Branch:    "renovate/x",
			CreatedAt: t1,
			UpdatedAt: t2,
			MergedAt:  &t2,
			ClosedAt:  &t2,
		},
		fakegithub.PR{
			RepoID:    1,
			Number:    3,
			Author:    bot,
			State:     fakegithub.PRClosed,
			Branch:    "renovate/x",
			CreatedAt: t1,
			UpdatedAt: t2,
			ClosedAt:  &t2,
		},
		// The search still lists #5 open, older than the webhook's close.
		fakegithub.PR{RepoID: 1, Number: 5, Author: bot, State: fakegithub.PROpen, Branch: "renovate/x", CreatedAt: t1, UpdatedAt: t1},
	)
	cfg := &config.Config{Apps: []*config.App{{Name: "boop-bot", AppID: 1, Endpoint: gh.BaseURL(), PrivateKey: config.NewSecret(gh.PEM)}}}
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	activities.New(&activities.Deps{Config: cfg, Store: store.New(worker, storetest.RoleWorker, nil)}).Register(env)
	reconcile := func() *workflows.ReconcilePRsResult {
		t.Helper()
		val, err := env.ExecuteActivity(workflows.ReconcilePullRequestsActivity,
			&workflows.ReconcilePRsInput{App: "boop-bot", InstallationID: 1, Account: "acme"})
		if err != nil {
			t.Fatalf("ReconcilePullRequests: %v", err)
		}
		var out workflows.ReconcilePRsResult
		if err := val.Get(&out); err != nil {
			t.Fatal(err)
		}
		return &out
	}

	if got := reconcile(); got.Open != 2 || got.Left != 2 || got.Synced != 4 {
		t.Errorf("first pass = %+v, want 2 open, 2 left, 4 synced", *got)
	}
	want := map[int]string{1: store.PRStateOpen, 2: store.PRStateMerged, 3: store.PRStateClosed, 5: store.PRStateClosed}
	check := func() {
		t.Helper()
		rows, err := owner.Query(t.Context(), "SELECT number, state FROM pull_requests WHERE repo_id = 1")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := make(map[int]string)
		for rows.Next() {
			var n int
			var s string
			if err := rows.Scan(&n, &s); err != nil {
				t.Fatal(err)
			}
			got[n] = s
		}
		if len(got) != len(want) {
			t.Errorf("pull_requests = %v, want %v", got, want)
		}
		for n, s := range want {
			if got[n] != s {
				t.Errorf("#%d state = %q, want %q", n, got[n], s)
			}
		}
	}
	check()
	if n := count(t, owner, "SELECT count(*) FROM events WHERE kind = 'pr.synced'"); n != 4 {
		t.Errorf("pr.synced events = %d, want 4", n)
	}

	// Only #1 is still open in the store, and GitHub lists it unchanged.
	if got := reconcile(); got.Left != 0 || got.Synced != 2 {
		t.Errorf("second pass = %+v, want nothing left and the 2 listed re-synced", *got)
	}
	check()
	if n := count(t, owner, "SELECT count(*) FROM events WHERE kind = 'pr.synced'"); n != 4 {
		t.Errorf("pr.synced events after a second pass = %d, want still 4", n)
	}
}

// TestReconcilePullRequests_SyncsDashboards: a dashboard Renovate last
// edited before the store existed, so no webhook carried it, reaches
// the store from the issue search; a bot issue that is not a dashboard
// does not. An unchanged dashboard is a duplicate on the next pass, and
// a rewrite replaces the items.
func TestReconcilePullRequests_SyncsDashboards(t *testing.T) {
	srv := storetest.Start(t)
	if _, err := store.Migrate(t.Context(), srv.OwnerDSN(), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	owner, err := store.Open(t.Context(), srv.OwnerDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	worker, err := store.Open(t.Context(), srv.RoleDSN(storetest.RoleWorker))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Close)

	t1 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	if _, err := owner.Exec(t.Context(),
		"INSERT INTO repositories (id, installation_id, owner, slug, updated_at) VALUES (1, 1, 'acme', 'acme/api', $1)", t1); err != nil {
		t.Fatal(err)
	}
	gh := fakegithub.New(t)
	gh.SetRepos(fakegithub.Repo{ID: 1, NodeID: "R_1", Slug: "acme/api", DefaultBranch: "main"})
	const bot = "boop-bot[bot]"
	body := func(items ...string) string {
		b := "This issue lists Renovate updates and detected dependencies.\n\n## Rate-Limited\n\n"
		for _, it := range items {
			b += " - [ ] <!-- unlimit-branch=" + it + " -->chore(deps): update " + it + "\n"
		}
		return b
	}
	gh.SetIssues(
		fakegithub.Issue{
			RepoID: 1, Number: 34, Author: bot, Title: "Dependency Dashboard", UpdatedAt: t1,
			Body: body("renovate/yaml-v4", "renovate/go-vcr-v4"),
		},
		fakegithub.Issue{RepoID: 1, Number: 40, Author: bot, Title: "Action required", UpdatedAt: t1, Body: "Renovate needs a config fix."},
	)
	cfg := &config.Config{Apps: []*config.App{{Name: "boop-bot", AppID: 1, Endpoint: gh.BaseURL(), PrivateKey: config.NewSecret(gh.PEM)}}}
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	activities.New(&activities.Deps{Config: cfg, Store: store.New(worker, storetest.RoleWorker, nil)}).Register(env)
	reconcile := func() *workflows.ReconcilePRsResult {
		t.Helper()
		val, err := env.ExecuteActivity(workflows.ReconcilePullRequestsActivity,
			&workflows.ReconcilePRsInput{App: "boop-bot", InstallationID: 1, Account: "acme"})
		if err != nil {
			t.Fatalf("ReconcilePullRequests: %v", err)
		}
		var out workflows.ReconcilePRsResult
		if err := val.Get(&out); err != nil {
			t.Fatal(err)
		}
		return &out
	}
	items := func() int {
		return count(t, owner, "SELECT count(*) FROM dashboard_items WHERE repo_id = 1 AND section = 'rate-limited'")
	}

	if got := reconcile(); got.Dashboards != 1 {
		t.Errorf("first pass = %+v, want 1 dashboard", *got)
	}
	if n := items(); n != 2 {
		t.Errorf("rate-limited items = %d, want 2", n)
	}
	if n := count(t, owner, "SELECT count(*) FROM dashboards WHERE repo_id = 1 AND issue_url = 'https://github.com/acme/api/issues/34'"); n != 1 {
		t.Errorf("dashboards with the issue URL = %d, want 1", n)
	}
	reconcile()
	if n := count(t, owner, "SELECT count(*) FROM events WHERE kind = 'dashboard.updated'"); n != 1 {
		t.Errorf("dashboard.updated events after an unchanged pass = %d, want 1", n)
	}

	gh.SetIssues(fakegithub.Issue{
		RepoID: 1, Number: 34, Author: bot, Title: "Dependency Dashboard", UpdatedAt: t1.Add(time.Hour),
		Body: body("renovate/go-vcr-v4"),
	})
	reconcile()
	if n := items(); n != 1 {
		t.Errorf("rate-limited items after a rewrite = %d, want 1", n)
	}
}
