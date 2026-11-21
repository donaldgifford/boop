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

package api_test

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	boopapi "github.com/donaldgifford/boop/internal/api"
	"github.com/donaldgifford/boop/internal/api/apitest"
	"github.com/donaldgifford/boop/internal/api/gen"
	"github.com/donaldgifford/boop/internal/api/oidctest"
	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/storetest"
	"github.com/donaldgifford/boop/internal/temporal/temporaltest"
	"github.com/donaldgifford/boop/internal/workflows"
)

// seedNow is the fixture's clock: every event is placed relative to it
// and the API reads with it.
var seedNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

const (
	guidReceived = "6a0c1f5e-0000-4000-8000-0000000000a1"
	guidFailed   = "6a0c1f5e-0000-4000-8000-0000000000b2"
	keyScheduled = "wf:repo/github/1:run-a:1"
	keyRecheck   = "wf:repo/github/3:run-a:1"
)

func ago(d time.Duration) time.Time { return seedNow.Add(-d) }

func ptrTime(t time.Time) *time.Time { return &t }

const day = 24 * time.Hour

// seedEvents is the fixture. Two installations: acme-web (10) with site
// (1) and cdn (2), acme-pay (20) with ledger (3).
//
//   - runs: r1 on site 2 h ago, Succeeded, scheduled, one lodash update;
//     r2 on ledger 3 h ago, Failed, rechecked, one lodash update;
//     r3 on cdn 30 h ago, Succeeded, no cause.
//   - pull requests: site #7 open 10 days, ledger #3 open 40 days, cdn #5
//     merged 2 days ago.
//   - one unticked dashboard item on site, 3 days old.
//   - deliveries: one received an hour ago, one failed (502) 30 min ago.
func seedEvents() []store.Event {
	inst := func(id int64, login string) store.Event {
		return store.Event{
			Key: "inst:" + login, OccurredAt: ago(60 * day), Kind: store.KindInstallationAdded, Source: store.SourceWebhook,
			InstallationID: id, Summary: login + " installed",
			Data: &store.Installation{V: 1, App: "boop-bot", AccountLogin: login, AccountType: "Organization"},
		}
	}
	repo := func(id, installation int64, owner, slug string) store.Event {
		return store.Event{
			Key: "onboard:" + slug, OccurredAt: ago(50 * day), Kind: store.KindRepoOnboarded, Source: store.SourceWorker,
			InstallationID: installation, RepoID: id, Summary: slug + " onboarded",
			Data: &store.Repository{V: 1, Owner: owner, Slug: slug, Profile: "default", Via: "discovery"},
		}
	}
	lodash := func(pr int) store.BranchUpdate {
		return store.BranchUpdate{
			Branch: "renovate/lodash-4.x", PackageFile: "package.json", PackageName: "lodash", Manager: "npm",
			Datasource: "npm", UpdateType: "minor", FromVersion: "4.17.20", ToVersion: "4.17.21", PRNumber: pr,
		}
	}
	run := func(id string, repoID, installation int64, owner, slug string, start time.Time, outcome, cause string,
		updates []store.BranchUpdate, deps []store.Dependency,
	) []store.Event {
		finish := start.Add(10 * time.Minute)
		base := store.Event{Source: store.SourceWorker, InstallationID: installation, RepoID: repoID, CauseKey: cause}
		started, finished := base, base
		started.Key, started.OccurredAt, started.Kind, started.Summary = "run:"+id+":started", start, store.KindRunStarted, "run started"
		started.Data = &store.RunStarted{V: 1, RunID: id, Owner: owner, Slug: slug, QueuedMS: 1000}
		finished.Key, finished.OccurredAt, finished.Kind, finished.Summary = "run:"+id+":finished", finish, store.KindRunFinished, "run done"
		finished.Data = &store.RunFinished{
			V: 1, RunID: id, Owner: owner, Slug: slug, StartedAt: start, FinishedAt: finish, Outcome: outcome,
			RepositoryResult: "done", RenovateVersion: "41.0.0",
			Spend:   map[string]store.Spend{"core": {Before: 5000, After: 4900}},
			Updates: updates, Dependencies: deps,
		}
		return []store.Event{started, finished}
	}
	pr := func(repoID, installation int64, number int, kind store.Kind, state string, opened time.Time, merged *time.Time) store.Event {
		at := opened
		if merged != nil {
			at = *merged
		}
		return store.Event{
			Key: "pr:" + string(kind) + ":" + string(rune('a'+number)), OccurredAt: at, Kind: kind, Source: store.SourceWebhook,
			InstallationID: installation, RepoID: repoID, PRNumber: number, Summary: "pull request",
			Data: &store.PullRequest{
				V: 1, Number: number, Branch: "renovate/lodash-4.x", State: state, Title: "Update lodash",
				Labels: []string{"dependencies"}, URL: "https://github.com/x/y/pull/1", OpenedAt: &opened, MergedAt: merged,
				UpdatedAt: at,
			},
		}
	}
	evs := []store.Event{
		inst(10, "acme-web"), inst(20, "acme-pay"),
		repo(1, 10, "acme-web", "acme-web/site"), repo(2, 10, "acme-web", "acme-web/cdn"), repo(3, 20, "acme-pay", "acme-pay/ledger"),
		{
			Key: keyScheduled, OccurredAt: ago(3 * time.Hour), Kind: store.KindRepoRunScheduled, Source: store.SourceWorker,
			InstallationID: 10, RepoID: 1, Summary: "next run", Data: &store.RunScheduled{V: 1, NextDue: ago(2 * time.Hour)},
		},
		{
			Key: keyRecheck, OccurredAt: ago(4 * time.Hour), Kind: store.KindActionRecheck, Source: store.SourceWorker,
			InstallationID: 20, RepoID: 3, Actor: store.Actor{Login: "alice", Type: "oidc"}, Summary: "recheck requested by alice",
			Data: &store.Recheck{V: 1, RequestID: "req-1"},
		},
		pr(1, 10, 7, store.KindPROpened, store.PRStateOpen, ago(10*day), nil),
		pr(3, 20, 3, store.KindPROpened, store.PRStateOpen, ago(40*day), nil),
		pr(2, 10, 5, store.KindPRMerged, store.PRStateMerged, ago(5*day), ptrTime(ago(2*day))),
		{
			Key: "dash:1", OccurredAt: ago(3 * day), Kind: store.KindDashboardUpdated, Source: store.SourceWebhook,
			InstallationID: 10, RepoID: 1, Summary: "dashboard updated",
			Data: &store.DashboardUpdated{V: 1, Items: []store.DashboardItem{{
				Marker: "approve-branch", Branch: "renovate/react-18.x", Section: "pending-approval",
				Title: "Update react to v18", Deps: []string{"react"},
			}}},
		},
		{
			Key: "delivery:" + guidReceived + ":received:1", OccurredAt: ago(time.Hour), Kind: store.KindDeliveryReceived,
			Source: store.SourceWebhook, InstallationID: 10, RepoID: 1, Summary: "pull_request delivered",
			Data: &store.DeliveryReceived{
				V: 1, DeliveryID: guidReceived, Attempt: 1, App: "boop-bot", Event: "pull_request", Action: "opened", Outcome: "routed",
			},
		},
		{
			Key: "audit:" + guidFailed + ":1:delivery.failed", OccurredAt: ago(30 * time.Minute), Kind: store.KindDeliveryFailed,
			Source: store.SourceAudit, InstallationID: 10, Summary: "push failed",
			Data: &store.DeliveryAudited{V: 1, DeliveryID: guidFailed, Attempt: 1, App: "boop-bot", Event: "push", GitHubStatus: 502},
		},
		{
			Key: "discovery:1", OccurredAt: ago(2 * time.Hour), Kind: store.KindDiscoveryFinished, Source: store.SourceWorker,
			Summary: "discovery finished",
			Data: &store.DiscoveryFinished{
				V: 1, App: "boop-bot", StartedAt: ago(2*time.Hour + time.Minute), FinishedAt: ago(2 * time.Hour), Seen: 3, Onboarded: 3,
			},
		},
	}
	return slices.Concat(evs,
		run("r1", 1, 10, "acme-web", "acme-web/site", ago(2*time.Hour), store.RunSucceeded, keyScheduled,
			[]store.BranchUpdate{lodash(7)},
			[]store.Dependency{{
				Manager: "npm", PackageFile: "package.json", PackageName: "lodash", Datasource: "npm",
				CurrentValue: "^4.17.20", CurrentVersion: "4.17.20",
			}}),
		run("r2", 3, 20, "acme-pay", "acme-pay/ledger", ago(3*time.Hour), store.RunFailed, keyRecheck,
			[]store.BranchUpdate{lodash(3)}, nil),
		run("r3", 2, 10, "acme-web", "acme-web/cdn", ago(30*time.Hour), store.RunSucceeded, "", nil, nil),
	)
}

// seeded is a migrated store holding the fixture, read through boopd_ro.
func seeded(t *testing.T) *store.Store {
	t.Helper()
	db := storetest.Start(t)
	if _, err := store.Migrate(t.Context(), db.OwnerDSN(), quiet); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	owner, err := store.Open(t.Context(), db.OwnerDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := store.New(owner, "boopd_owner", nil).Append(t.Context(), seedEvents()...); err != nil {
		t.Fatalf("Append: %v", err)
	}
	ro, err := store.OpenReadOnly(t.Context(), db.RoleDSN(storetest.RoleRO))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ro.Close)
	return store.New(ro, "boopd_ro", nil)
}

// seededAPI is the API over the seeded store with OIDC, at seedNow.
type seededAPI struct {
	issuer         *oidctest.Issuer
	client         *apitest.Client
	platform, team string
}

func newSeededAPI(t *testing.T, st *store.Store, opts ...func(*boopapi.Options)) *seededAPI {
	t.Helper()
	iss := oidctest.Start(t, audience)
	path := filepath.Join(t.TempDir(), "authz.yaml")
	if err := os.WriteFile(path, []byte(authzYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	authz, err := boopapi.LoadAuthzConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	authn := boopapi.NewAuthenticator(&boopapi.AuthnConfig{
		Issuer: iss.URL, Audience: audience, NameClaim: "preferred_username", GroupsClaim: "groups",
	}, quiet, nil)
	authn.Start(t.Context())
	o := &boopapi.Options{
		Reader: st, Authn: authn, Authz: authz, Logger: quiet, Now: func() time.Time { return seedNow },
	}
	for _, opt := range opts {
		opt(o)
	}
	return &seededAPI{
		issuer: iss, client: apitest.New(t, o), platform: iss.Valid(t, "platform"), team: iss.Valid(t, "team-web"),
	}
}

func get[T any](t *testing.T, a *seededAPI, path, token string) T {
	t.Helper()
	resp := a.client.Get(path, token)
	if resp.Status != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, resp.Status, resp.Body)
	}
	return decode[T](t, resp)
}

// TestAPI_EndpointsOverSeed reads every endpoint over the fixture, as an
// every-owner caller and as team-web (acme-web only). apitest validates
// each response against the contract.
func TestAPI_EndpointsOverSeed(t *testing.T) {
	st := seeded(t)
	a := newSeededAPI(t, st)

	t.Run("me", func(t *testing.T) {
		me := get[gen.Me](t, a, "/me", a.platform)
		if !slices.Equal(me.Owners, []string{"acme-pay", "acme-web"}) {
			t.Errorf("owners = %v", me.Owners)
		}
	})

	t.Run("installations", func(t *testing.T) {
		list := get[gen.InstallationList](t, a, "/installations", a.platform)
		if len(list.Items) != 2 || list.Items[0].Id != 10 || list.Items[0].Repositories != 2 {
			t.Fatalf("installations = %+v", list.Items)
		}
		if c := list.Items[0].Core; c == nil || c.Remaining != 4900 || c.UsedSinceRunStart == nil || *c.UsedSinceRunStart != 100 {
			t.Errorf("core budget = %+v, want 4900 remaining after 100", c)
		}
		if team := get[gen.InstallationList](t, a, "/installations", a.team); len(team.Items) != 1 || team.Items[0].Owner != "acme-web" {
			t.Errorf("team installations = %+v", team.Items)
		}
	})

	t.Run("repositories", func(t *testing.T) {
		list := get[gen.RepositoryList](t, a, "/repositories", a.platform)
		slugs := make([]string, 0, len(list.Items))
		for _, r := range list.Items {
			slugs = append(slugs, r.Slug)
		}
		if !slices.Equal(slugs, []string{"acme-pay/ledger", "acme-web/cdn", "acme-web/site"}) {
			t.Errorf("slugs = %v", slugs)
		}
		team := get[gen.RepositoryList](t, a, "/repositories", a.team)
		if len(team.Items) != 2 {
			t.Errorf("team repositories = %d, want 2", len(team.Items))
		}
		if q := get[gen.RepositoryList](t, a, "/repositories?q=ACME-WEB/S", a.platform); len(q.Items) != 1 {
			t.Errorf("q=ACME-WEB/S = %d items, want site", len(q.Items))
		}
		if open := get[gen.RepositoryList](t, a, "/repositories?has_open_prs=true", a.platform); len(open.Items) != 2 {
			t.Errorf("has_open_prs = %d, want 2", len(open.Items))
		}
	})

	t.Run("cursor pages and binds to filters", func(t *testing.T) {
		first := get[gen.RepositoryList](t, a, "/repositories?limit=2", a.platform)
		if len(first.Items) != 2 || first.NextCursor == nil {
			t.Fatalf("page 1 = %d items, next %v", len(first.Items), first.NextCursor)
		}
		c := url.QueryEscape(*first.NextCursor)
		second := get[gen.RepositoryList](t, a, "/repositories?limit=2&cursor="+c, a.platform)
		if len(second.Items) != 1 || second.NextCursor != nil || second.Items[0].Slug != "acme-web/site" {
			t.Errorf("page 2 = %+v, next %v", second.Items, second.NextCursor)
		}
		if resp := a.client.Get("/repositories?limit=2&state=idle&cursor="+c, a.platform); resp.Status != http.StatusBadRequest {
			t.Errorf("cursor under other filters = %d, want 400", resp.Status)
		}
		if resp := a.client.Get("/repositories?cursor=garbage", a.platform); resp.Status != http.StatusBadRequest {
			t.Errorf("malformed cursor = %d, want 400", resp.Status)
		}
	})

	t.Run("repository", func(t *testing.T) {
		r := get[gen.RepositoryDetail](t, a, "/repositories/1", a.platform)
		if r.Live || r.OpenPrs != 1 || r.Waiting != 1 || r.LastRunId == nil || *r.LastRunId != "r1" {
			t.Errorf("site = %+v", r)
		}
		if resp := a.client.Get("/repositories/3", a.team); resp.Status != http.StatusNotFound {
			t.Errorf("ledger as team-web = %d, want 404", resp.Status)
		}
	})

	t.Run("repository events", func(t *testing.T) {
		evs := get[gen.EventList](t, a, "/repositories/1/events", a.platform)
		if len(evs.Items) == 0 || evs.Items[0].Kind != "delivery.received" {
			t.Fatalf("site events = %+v", evs.Items)
		}
		runs := get[gen.EventList](t, a, "/repositories/1/events?kind=run.", a.platform)
		if len(runs.Items) != 2 || runs.Items[0].Kind != "run.finished" {
			t.Errorf("kind=run. = %+v", runs.Items)
		}
	})

	t.Run("runs", func(t *testing.T) {
		site := get[gen.RunList](t, a, "/repositories/1/runs", a.platform)
		if len(site.Items) != 1 || site.Items[0].Cause != "schedule" || site.Items[0].Updates != 1 {
			t.Errorf("site runs = %+v", site.Items)
		}
		all := get[gen.RunList](t, a, "/runs", a.platform)
		ids := make([]string, 0, len(all.Items))
		for _, r := range all.Items {
			ids = append(ids, r.Id)
		}
		if !slices.Equal(ids, []string{"r1", "r2", "r3"}) {
			t.Errorf("runs = %v, want newest first", ids)
		}
		if re := get[gen.RunList](t, a, "/runs?cause=recheck", a.platform); len(re.Items) != 1 || re.Items[0].Id != "r2" {
			t.Errorf("cause=recheck = %+v", re.Items)
		}
		if cdn := get[gen.RunList](t, a, "/runs?cause=unknown", a.platform); len(cdn.Items) != 1 || cdn.Items[0].Id != "r3" {
			t.Errorf("cause=unknown = %+v", cdn.Items)
		}
		if team := get[gen.RunList](t, a, "/runs", a.team); len(team.Items) != 2 {
			t.Errorf("team runs = %d, want 2", len(team.Items))
		}
	})

	t.Run("dependencies", func(t *testing.T) {
		deps := get[gen.DependencyList](t, a, "/repositories/1/dependencies", a.platform)
		if len(deps.Items) != 1 || deps.Items[0].Purl == nil || *deps.Items[0].Purl != "pkg:npm/lodash@4.17.20" {
			t.Fatalf("site dependencies = %+v", deps.Items)
		}
		if byPurl := get[gen.DependencyList](t, a, "/dependencies?purl=pkg:npm/lodash", a.platform); len(byPurl.Items) != 1 {
			t.Errorf("dependencies?purl = %d, want 1", len(byPurl.Items))
		}
	})

	t.Run("updates and packages", func(t *testing.T) {
		ups := get[gen.UpdateList](t, a, "/updates?purl="+url.QueryEscape("pkg:npm/lodash@4.17.20"), a.platform)
		if len(ups.Items) != 2 {
			t.Errorf("updates = %d, want 2", len(ups.Items))
		}
		if team := get[gen.UpdateList](t, a, "/updates", a.team); len(team.Items) != 1 {
			t.Errorf("team updates = %d, want 1", len(team.Items))
		}
		pkgs := get[gen.PackageList](t, a, "/packages", a.platform)
		if len(pkgs.Items) != 1 || pkgs.Items[0].Purl != "pkg:npm/lodash" || pkgs.Items[0].Repositories != 2 {
			t.Errorf("packages = %+v", pkgs.Items)
		}
	})

	t.Run("pull requests", func(t *testing.T) {
		open := get[gen.PullRequestList](t, a, "/pull-requests?state=open", a.platform)
		if len(open.Items) != 2 || open.Items[0].Number != 3 || open.Items[1].Number != 7 {
			t.Fatalf("open = %+v, want ledger #3 then site #7", open.Items)
		}
		if len(open.Items[1].Updates) != 1 || open.Items[1].Updates[0].PackageName != "lodash" {
			t.Errorf("site #7 updates = %+v", open.Items[1].Updates)
		}
		if old := get[gen.PullRequestList](t, a, "/pull-requests?older_than=20", a.platform); len(old.Items) != 1 {
			t.Errorf("older_than=20 = %d, want 1", len(old.Items))
		}
		if purl := get[gen.PullRequestList](t, a, "/pull-requests?purl=pkg:npm/lodash", a.platform); len(purl.Items) != 2 {
			t.Errorf("purl = %d, want the two open ones", len(purl.Items))
		}
		if team := get[gen.PullRequestList](t, a, "/pull-requests", a.team); len(team.Items) != 2 {
			t.Errorf("team pull requests = %d, want site and cdn", len(team.Items))
		}
		detail := get[gen.PullRequestDetail](t, a, "/pull-requests/1/7", a.platform)
		if len(detail.Timeline) != 1 || detail.Timeline[0].Kind != "pr.opened" {
			t.Errorf("timeline = %+v", detail.Timeline)
		}
		if resp := a.client.Get("/pull-requests/3/3", a.team); resp.Status != http.StatusNotFound {
			t.Errorf("ledger #3 as team-web = %d, want 404", resp.Status)
		}
	})

	t.Run("dashboard items", func(t *testing.T) {
		items := get[gen.DashboardItemList](t, a, "/dashboard-items", a.platform)
		if len(items.Items) != 1 || items.Items[0].Marker != "approve-branch" || !slices.Equal(items.Items[0].Deps, []string{"react"}) {
			t.Errorf("items = %+v", items.Items)
		}
		if older := get[gen.DashboardItemList](t, a, "/dashboard-items?waiting_longer_than=5", a.platform); len(older.Items) != 0 {
			t.Errorf("waiting_longer_than=5 = %d, want 0", len(older.Items))
		}
	})

	t.Run("events and deliveries", func(t *testing.T) {
		inst := get[gen.EventList](t, a, "/events?kind=installation.", a.platform)
		if len(inst.Items) != 2 {
			t.Errorf("installation events = %d, want 2", len(inst.Items))
		}
		if team := get[gen.EventList](t, a, "/events?kind=installation.", a.team); len(team.Items) != 0 {
			t.Errorf("team sees %d installation events, want none", len(team.Items))
		}
		if alice := get[gen.EventList](t, a, "/events?actor=alice", a.platform); len(alice.Items) != 1 {
			t.Errorf("actor=alice = %d, want 1", len(alice.Items))
		}
		caused := get[gen.EventList](t, a, "/events?cause_key="+url.QueryEscape(keyRecheck), a.platform)
		if len(caused.Items) != 2 {
			t.Errorf("caused by the recheck = %d, want r2's two", len(caused.Items))
		}
		dels := get[gen.DeliveryList](t, a, "/deliveries", a.platform)
		if len(dels.Items) != 2 || dels.Items[0].DeliveryId.String() != guidFailed {
			t.Errorf("deliveries = %+v", dels.Items)
		}
		if failed := get[gen.DeliveryList](t, a, "/deliveries?github_status=502", a.platform); len(failed.Items) != 1 {
			t.Errorf("github_status=502 = %d, want 1", len(failed.Items))
		}
		if resp := a.client.Get("/deliveries", a.team); resp.Status != http.StatusForbidden {
			t.Errorf("deliveries as team-web = %d, want 403", resp.Status)
		}
	})
}

// TestAPI_EstateMatchesFixture checks the estate against numbers worked
// out by hand from seedEvents.
func TestAPI_EstateMatchesFixture(t *testing.T) {
	a := newSeededAPI(t, seeded(t))
	e := get[gen.Estate](t, a, "/estate", a.platform)
	if e.Repositories != 3 || e.OpenPrs != 2 || e.Waiting != 1 || e.Runs24h != 2 || e.LostDeliveries24h != 1 {
		t.Errorf("estate = repos %d open %d waiting %d runs %d lost %d, want 3 2 1 2 1",
			e.Repositories, e.OpenPrs, e.Waiting, e.Runs24h, e.LostDeliveries24h)
	}
	if e.OldestOpenPrOpenedAt == nil || !e.OldestOpenPrOpenedAt.Equal(ago(40*day)) {
		t.Errorf("oldest open = %v, want 40 days ago", e.OldestOpenPrOpenedAt)
	}
	// r1 Succeeded and r2 Failed within the day: 50.0%.
	if e.RunSuccessRate24h == nil || *e.RunSuccessRate24h != 50 {
		t.Errorf("success rate = %v, want 50", e.RunSuccessRate24h)
	}
	if e.MergedInRange == nil || *e.MergedInRange != 1 {
		t.Errorf("merged in 30d = %v, want 1", e.MergedInRange)
	}
	ages := make([]int, 0, len(e.AgeBuckets))
	for _, b := range e.AgeBuckets {
		ages = append(ages, b.Count)
	}
	if !slices.Equal(ages, []int{0, 0, 1, 1, 0}) {
		t.Errorf("age buckets = %v, want #7 in 7-30d and #3 in 30-90d", ages)
	}
	if len(e.Ecosystems) != 1 || e.Ecosystems[0] != (gen.Bucket{Label: "npm", Count: 2}) {
		t.Errorf("ecosystems = %+v", e.Ecosystems)
	}
	if len(e.TopPackages) != 1 || e.TopPackages[0].Repositories != 2 {
		t.Errorf("top packages = %+v", e.TopPackages)
	}

	team := get[gen.Estate](t, a, "/estate?owner=acme-web&range=7d", a.platform)
	if team.Repositories != 2 || team.OpenPrs != 1 || team.Runs24h != 1 || team.Owner == nil || *team.Owner != "acme-web" {
		t.Errorf("acme-web estate = %+v", team)
	}
	if resp := a.client.Get("/estate?range=2d", a.platform); resp.Status != http.StatusBadRequest {
		t.Errorf("range=2d = %d, want 400", resp.Status)
	}

	runs := get[gen.EstateSeries](t, a, "/estate/series?metric=runs&range=1d", a.platform)
	total := 0
	for _, p := range runs.Points {
		total += p.Value
	}
	if runs.Bucket != "hour" || len(runs.Points) != 24 || total != 2 {
		t.Errorf("runs series = %s, %d points, total %d; want 24 hours holding r1 and r2", runs.Bucket, len(runs.Points), total)
	}
	open := get[gen.EstateSeries](t, a, "/estate/series?metric=open_prs&range=90d", a.platform)
	if last := open.Points[len(open.Points)-1]; open.Bucket != "day" || len(open.Points) != 90 || last.Value != 2 {
		t.Errorf("open_prs series = %s, %d points, last %+v", open.Bucket, len(open.Points), last)
	}
	if resp := a.client.Get("/estate/series?metric=nope", a.platform); resp.Status != http.StatusBadRequest {
		t.Errorf("metric=nope = %d, want 400", resp.Status)
	}
}

// TestAPI_StatusOverSeed: the status page's fleet numbers come from the
// store.
func TestAPI_StatusOverSeed(t *testing.T) {
	st := seeded(t)
	page := boopapi.NewStatusPage(&boopapi.StatusOptions{Reader: st, Version: "test", Now: func() time.Time { return seedNow }})
	page.Refresh(t.Context())
	a := newSeededAPI(t, st, func(o *boopapi.Options) { o.Status = page })
	s := get[gen.Status](t, a, "/status", "")
	if s.Fleet != (gen.StatusFleet{Repositories: 3, OpenPrs: 2, Runs24h: 2}) || s.Version != "test" {
		t.Errorf("status = %+v", s)
	}
}

// recheckWorkflow stands in for RepoWorkflow: it answers the state Query
// and returns the first recheck signal it receives.
func recheckWorkflow(ctx workflow.Context) (workflows.Recheck, error) {
	start := workflow.Now(ctx)
	if err := workflow.SetQueryHandler(ctx, workflows.StateQuery, func() (workflows.RepoView, error) {
		return workflows.RepoView{Phase: workflows.PhaseRunning, RunningSince: start}, nil
	}); err != nil {
		return workflows.Recheck{}, err
	}
	var rc workflows.Recheck
	workflow.GetSignalChannel(ctx, workflows.RecheckSignal).Receive(ctx, &rc)
	return rc, nil
}

// TestAPI_RecheckAgainstTheDevServer: 202 with the caller as actor and
// the request ID in the signal, the dedupe within the minute, 409 with
// no workflow, 503 with Temporal away, and the live state Query.
func TestAPI_RecheckAgainstTheDevServer(t *testing.T) {
	st := seeded(t)
	srv := temporaltest.Start(t)
	const queue = "api-recheck"
	w := worker.New(srv.Client, queue, worker.Options{})
	w.RegisterWorkflowWithOptions(recheckWorkflow, workflow.RegisterOptions{Name: "recheck-stand-in"})
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Stop)
	run, err := srv.Client.ExecuteWorkflow(t.Context(), client.StartWorkflowOptions{
		ID: workflows.RepoWorkflowID(1), TaskQueue: queue,
	}, "recheck-stand-in")
	if err != nil {
		t.Fatal(err)
	}
	a := newSeededAPI(t, st, func(o *boopapi.Options) { o.Temporal = srv.Client })

	live := get[gen.RepositoryDetail](t, a, "/repositories/1", a.platform)
	if !live.Live || live.Phase == nil || *live.Phase != workflows.PhaseRunning || live.RunningSince == nil {
		t.Errorf("live site = %+v, want running from the Query", live)
	}

	resp := a.client.Post("/repositories/1/recheck", a.issuer.Mint(t, &oidctest.Token{Groups: []string{"platform"}, Name: "Bob"}))
	if resp.Status != http.StatusAccepted {
		t.Fatalf("recheck = %d: %s", resp.Status, resp.Body)
	}
	acc := decode[gen.RecheckAccepted](t, resp)
	if acc.Deduplicated || acc.RequestId == "" {
		t.Errorf("accepted = %+v", acc)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var got workflows.Recheck
	if err := run.Get(ctx, &got); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if got.Actor == "" || got.RequestID != acc.RequestId {
		t.Errorf("signal = %+v, want an actor and request %s", got, acc.RequestId)
	}

	again := decode[gen.RecheckAccepted](t, a.client.Post("/repositories/1/recheck", a.platform))
	if !again.Deduplicated || again.RequestId != acc.RequestId {
		t.Errorf("second recheck = %+v, want deduplicated onto %s", again, acc.RequestId)
	}
	missing := a.client.Post("/repositories/3/recheck", a.platform)
	if p := decode[gen.Problem](t, missing); missing.Status != http.StatusConflict || p.Type != "workflow_missing" {
		t.Errorf("no workflow = %d %s, want 409 workflow_missing", missing.Status, p.Type)
	}

	away, err := client.NewLazyClient(client.Options{HostPort: "127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	defer away.Close()
	b := newSeededAPI(t, st, func(o *boopapi.Options) { o.Temporal = away })
	if resp := b.client.Post("/repositories/2/recheck", b.platform); resp.Status != http.StatusServiceUnavailable {
		t.Errorf("Temporal away = %d, want 503", resp.Status)
	}
	if r := get[gen.RepositoryDetail](t, b, "/repositories/2", b.platform); r.Live {
		t.Error("live with Temporal away")
	}
}
