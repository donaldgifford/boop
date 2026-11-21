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

package store_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/storetest"
)

var (
	base    = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	setupAt = base.Add(-time.Hour)
	olderAt = base
	newerAt = base.Add(time.Hour)
)

// ev builds an event on installation 1.
func ev(kind store.Kind, key string, at time.Time, repoID int64, data store.Data) store.Event {
	return store.Event{
		Key: key, OccurredAt: at, Kind: kind, Source: store.SourceWorker,
		InstallationID: 1, RepoID: repoID, Summary: string(kind), Data: data,
	}
}

// snapshot returns the rows of table matching where, as sorted JSON.
func snapshot(t *testing.T, p *pgxpool.Pool, table, where string) string {
	t.Helper()
	q := fmt.Sprintf("SELECT coalesce(json_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text), '[]')::text FROM %s t WHERE %s", table, where)
	var s string
	if err := p.QueryRow(t.Context(), q).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s
}

type view struct{ table, where string }

type kindCase struct {
	name  string
	role  string
	setup []store.Event
	newer store.Event
	older store.Event
	views []view
	want  string // in the newer snapshot; empty for timeline-only kinds
}

func uuid(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", n) }

func repoView(id int64) view { return view{"repositories", fmt.Sprintf("id = %d", id)} }

func kindCases() []kindCase {
	var cases []kindCase
	add := func(c kindCase) { cases = append(cases, c) }
	onboard := func(id int64) store.Event {
		return ev(
			store.KindRepoOnboarded,
			fmt.Sprintf("setup/%d", id),
			setupAt,
			id,
			&store.Repository{V: 1, Owner: "acme", Slug: fmt.Sprintf("acme/r%d", id)},
		)
	}

	// Deliveries.
	add(kindCase{
		name: "delivery.received",
		role: storetest.RoleIngest,
		newer: ev(
			store.KindDeliveryReceived,
			"dr/new",
			newerAt,
			0,
			&store.DeliveryReceived{V: 1, DeliveryID: uuid(1), Attempt: 1, App: "boop-bot", Event: "issues", Outcome: "routed"},
		),
		older: ev(
			store.KindDeliveryReceived,
			"dr/old",
			olderAt,
			0,
			&store.DeliveryReceived{V: 1, DeliveryID: uuid(1), Attempt: 1, App: "boop-bot", Event: "issues", Outcome: "ignored"},
		),
		views: []view{{"webhook_deliveries", "delivery_id = '" + uuid(1) + "'"}},
		want:  `"routed"`,
	})
	for i, kind := range []store.Kind{store.KindDeliveryFailed, store.KindDeliveryMissed, store.KindDeliveryRedelivered} {
		id := uuid(10 + i)
		add(kindCase{
			name: string(kind),
			role: storetest.RoleWorker,
			newer: ev(
				kind,
				string(kind)+"/new",
				newerAt,
				0,
				&store.DeliveryAudited{V: 1, DeliveryID: id, Attempt: 2, App: "boop-bot", Event: "pull_request", GitHubStatus: 502},
			),
			older: ev(
				kind,
				string(kind)+"/old",
				olderAt,
				0,
				&store.DeliveryAudited{V: 1, DeliveryID: id, Attempt: 2, App: "boop-bot", Event: "pull_request", GitHubStatus: 200},
			),
			views: []view{{"webhook_deliveries", "delivery_id = '" + id + "'"}},
			want:  `"github_status": 502`,
		})
	}

	// Pull requests: the guard is GitHub's updated_at.
	prKinds := []store.Kind{
		store.KindPROpened, store.KindPREdited, store.KindPRClosed, store.KindPRMerged,
		store.KindPRReopened, store.KindPRSynchronized, store.KindPRSynced,
	}
	for i, kind := range prKinds {
		repo := int64(100 + i)
		pr := func(title string, at time.Time) *store.PullRequest {
			return &store.PullRequest{
				V:         1,
				Number:    7,
				Branch:    "renovate/x",
				State:     store.PRStateOpen,
				Title:     title,
				Labels:    []string{"deps"},
				UpdatedAt: at,
			}
		}
		add(kindCase{
			name: string(kind), role: storetest.RoleIngest,
			newer: ev(kind, string(kind)+"/new", newerAt, repo, pr("bump x to 2", newerAt)),
			older: ev(kind, string(kind)+"/old", olderAt, repo, pr("bump x to 1", olderAt)),
			views: []view{{"pull_requests", fmt.Sprintf("repo_id = %d", repo)}}, want: "bump x to 2",
		})
	}
	add(kindCase{
		name: "pr.checkbox", role: storetest.RoleIngest,
		newer: ev(store.KindPRCheckbox, "cb/new", newerAt, 120, &store.PRCheckbox{V: 1, Number: 7, Box: "rebase"}),
		older: ev(store.KindPRCheckbox, "cb/old", olderAt, 120, &store.PRCheckbox{V: 1, Number: 7, Box: "retry"}),
		views: []view{{"pull_requests", "repo_id = 120"}},
	})

	// Dashboard.
	item := func(branch, title string) store.DashboardItem {
		return store.DashboardItem{Marker: "approve-branch", Branch: branch, Section: "Pending Approval", Title: title, Deps: []string{"x"}}
	}
	add(kindCase{
		name: "dashboard.updated",
		role: storetest.RoleIngest,
		newer: ev(
			store.KindDashboardUpdated,
			"du/new",
			newerAt,
			130,
			&store.DashboardUpdated{V: 1, Items: []store.DashboardItem{item("renovate/a", "a new")}},
		),
		older: ev(
			store.KindDashboardUpdated,
			"du/old",
			olderAt,
			130,
			&store.DashboardUpdated{V: 1, Items: []store.DashboardItem{item("renovate/b", "b old")}},
		),
		views: []view{{"dashboard_items", "repo_id = 130"}, {"dashboards", "repo_id = 130"}},
		want:  "a new",
	})
	tick := func(key, login string, at time.Time) store.Event {
		e := ev(store.KindDashboardTicked, key, at, 131, &store.DashboardTicked{V: 1, Marker: "approve-branch", Branch: "renovate/a"})
		e.Actor = store.Actor{Login: login, Type: store.ActorUser}
		return e
	}
	add(kindCase{
		name: "dashboard.ticked",
		role: storetest.RoleIngest,
		setup: []store.Event{
			ev(
				store.KindDashboardUpdated,
				"dt/setup",
				setupAt,
				131,
				&store.DashboardUpdated{V: 1, Items: []store.DashboardItem{item("renovate/a", "a")}},
			),
		},
		newer: tick("dt/new", "alice", newerAt),
		older: tick("dt/old", "bob", olderAt),
		views: []view{{"dashboard_items", "repo_id = 131"}},
		want:  `"ticked_by": "alice"`,
	})

	// Repository lifecycle and schedule.
	for i, kind := range []store.Kind{store.KindRepoOnboarded, store.KindRepoOffboarded, store.KindRepoRefreshed} {
		repo := int64(140 + i)
		add(kindCase{
			name: string(kind), role: storetest.RoleWorker,
			newer: ev(kind, string(kind)+"/new", newerAt, repo, &store.Repository{V: 1, Owner: "acme", Slug: "acme/new", Profile: "python"}),
			older: ev(kind, string(kind)+"/old", olderAt, repo, &store.Repository{V: 1, Owner: "acme", Slug: "acme/old", Profile: "default"}),
			views: []view{repoView(repo)}, want: "acme/new",
		})
	}
	add(kindCase{
		name:  "repo.run-scheduled",
		role:  storetest.RoleWorker,
		setup: []store.Event{onboard(150)},
		newer: ev(
			store.KindRepoRunScheduled,
			"rs/new",
			newerAt,
			150,
			&store.RunScheduled{V: 1, NextDue: time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)},
		),
		older: ev(
			store.KindRepoRunScheduled,
			"rs/old",
			olderAt,
			150,
			&store.RunScheduled{V: 1, NextDue: time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)},
		),
		views: []view{repoView(150)},
		want:  "2026-11-23T",
	})

	// Runs.
	add(kindCase{
		name:  "run.started",
		role:  storetest.RoleWorker,
		setup: []store.Event{onboard(160)},
		newer: ev(
			store.KindRunStarted,
			"rst/new",
			newerAt,
			160,
			&store.RunStarted{V: 1, RunID: "run-160", Owner: "acme", Slug: "acme/r160", QueuedMS: 1500},
		),
		older: ev(
			store.KindRunStarted,
			"rst/old",
			olderAt,
			160,
			&store.RunStarted{V: 1, RunID: "run-160", Owner: "acme", Slug: "acme/r160", QueuedMS: 9},
		),
		views: []view{repoView(160), {"runs", "repo_id = 160"}},
		want:  `"queued_ms": 1500`,
	})
	finished := func(runID, outcome, to string, at time.Time) *store.RunFinished {
		return &store.RunFinished{
			V:                1,
			RunID:            runID,
			Owner:            "acme",
			Slug:             "acme/r161",
			StartedAt:        at.Add(-time.Minute),
			FinishedAt:       at,
			Outcome:          outcome,
			RepositoryResult: "done",
			Spend:            map[string]store.Spend{"core": {Before: 5000, After: 4900}},
			Updates: []store.BranchUpdate{{
				Branch: "renovate/x", PackageFile: "go.mod", PackageName: "example.com/x", Manager: "gomod",
				Datasource: "go", FromVersion: "v1.0.0", ToVersion: to,
			}},
			Dependencies: []store.Dependency{
				{Manager: "gomod", PackageFile: "go.mod", PackageName: "example.com/x", Datasource: "go", CurrentValue: "v1.0.0"},
			},
		}
	}
	add(kindCase{
		name: "run.finished", role: storetest.RoleWorker,
		setup: []store.Event{onboard(161)},
		newer: ev(store.KindRunFinished, "rf/new", newerAt, 161, finished("run-new", store.RunSucceeded, "v1.2.0", newerAt)),
		older: ev(store.KindRunFinished, "rf/old", olderAt, 161, finished("run-old", store.RunFailed, "v1.1.0", olderAt)),
		views: []view{repoView(161), {"branch_updates", "repo_id = 161"}, {"dependencies", "repo_id = 161"}, {"runs", "id = 'run-new'"}},
		want:  "v1.2.0",
	})
	states := map[store.Kind]string{
		store.KindRunStalled:        store.RepoStateStalled,
		store.KindRunCapacityDenied: store.RepoStateWaiting,
		store.KindRunBudgetWait:     store.RepoStateRateLimited,
	}
	for i, kind := range []store.Kind{store.KindRunStalled, store.KindRunCapacityDenied, store.KindRunBudgetWait} {
		repo := int64(170 + i)
		add(kindCase{
			name: string(kind), role: storetest.RoleWorker,
			setup: []store.Event{onboard(repo)},
			newer: ev(kind, string(kind)+"/new", newerAt, repo, &store.RepoState{V: 1}),
			older: ev(kind, string(kind)+"/old", olderAt, repo, &store.RepoState{V: 1}),
			views: []view{repoView(repo)}, want: `"state": "` + states[kind] + `"`,
		})
	}

	// Installations.
	instKinds := []store.Kind{store.KindInstallationAdded, store.KindInstallationSusp, store.KindInstallationUnsusp, store.KindInstallationRemove}
	for i, kind := range instKinds {
		inst := int64(200 + i)
		mk := func(key, login string, at time.Time) store.Event {
			e := ev(kind, key, at, 0, &store.Installation{V: 1, App: "boop-bot", AccountLogin: login, AccountType: "Organization"})
			e.InstallationID = inst
			e.Source = store.SourceWebhook
			return e
		}
		add(kindCase{
			name: string(kind), role: storetest.RoleIngest,
			newer: mk(string(kind)+"/new", "acme", newerAt),
			older: mk(string(kind)+"/old", "old-name", olderAt),
			views: []view{{"installations", fmt.Sprintf("id = %d", inst)}}, want: `"acme"`,
		})
	}

	// Discovery and timeline-only actions.
	pass := func(seen int, at time.Time) *store.DiscoveryFinished {
		return &store.DiscoveryFinished{V: 1, App: "app-disc", StartedAt: setupAt, FinishedAt: at, Seen: seen, Onboarded: 2, ProbeCost: 3}
	}
	add(kindCase{
		name: "discovery.finished", role: storetest.RoleWorker,
		newer: ev(store.KindDiscoveryFinished, "df/new", newerAt, 0, pass(42, newerAt)),
		older: ev(store.KindDiscoveryFinished, "df/old", olderAt, 0, pass(7, olderAt)),
		views: []view{{"discovery_passes", "app = 'app-disc'"}}, want: `"seen": 42`,
	})
	add(kindCase{
		name: "action.recheck", role: storetest.RoleWorker,
		newer: ev(store.KindActionRecheck, "ar/new", newerAt, 190, &store.Recheck{V: 1, RequestID: "a"}),
		older: ev(store.KindActionRecheck, "ar/old", olderAt, 190, &store.Recheck{V: 1, RequestID: "b"}),
		views: []view{repoView(190)},
	})
	return cases
}

func TestApply_EveryKind(t *testing.T) {
	srv := migrated(t)
	owner := pool(t, srv.OwnerDSN())
	stores := map[string]*store.Store{
		storetest.RoleWorker: store.New(pool(t, srv.RoleDSN(storetest.RoleWorker)), storetest.RoleWorker, nil),
		storetest.RoleIngest: store.New(pool(t, srv.RoleDSN(storetest.RoleIngest)), storetest.RoleIngest, nil),
	}

	cases := kindCases()
	covered := make(map[store.Kind]bool)
	for _, c := range cases {
		covered[c.newer.Kind] = true
		t.Run(c.name, func(t *testing.T) {
			s := stores[c.role]
			snap := func() string {
				parts := make([]string, 0, len(c.views))
				for _, v := range c.views {
					parts = append(parts, snapshot(t, owner, v.table, v.where))
				}
				return strings.Join(parts, "\n")
			}
			if len(c.setup) > 0 {
				if err := s.Append(t.Context(), c.setup...); err != nil {
					t.Fatalf("setup: %v", err)
				}
			}
			before := snap()

			if err := s.Append(t.Context(), c.newer); err != nil {
				t.Fatalf("Append(newer): %v", err)
			}
			applied := snap()
			if c.want == "" {
				if applied != before {
					t.Errorf("timeline-only kind changed a projection:\n got %s\nwant %s", applied, before)
				}
			} else if !strings.Contains(applied, c.want) {
				t.Errorf("after apply = %s, want it to contain %s", applied, c.want)
			}

			if err := s.Append(t.Context(), c.newer); err != nil {
				t.Fatalf("Append(newer) again: %v", err)
			}
			again := c.newer
			again.Key += "/replay"
			if err := s.Append(t.Context(), again); err != nil {
				t.Fatalf("Append(replay): %v", err)
			}
			if got := snap(); got != applied {
				t.Errorf("re-apply changed the projection:\n got %s\nwant %s", got, applied)
			}

			if err := s.Append(t.Context(), c.older); err != nil {
				t.Fatalf("Append(older): %v", err)
			}
			if got := snap(); got != applied {
				t.Errorf("older event changed the projection:\n got %s\nwant %s", got, applied)
			}
		})
	}
	for _, k := range store.Kinds() {
		if !covered[k] {
			t.Errorf("kind %s has no apply test", k)
		}
	}
}
