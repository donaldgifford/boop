//go:build integration

package activities_test

import (
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/storetest"
	"github.com/donaldgifford/boop/internal/workflows"
)

// storeFixture starts Postgres, migrates it and returns activities
// recording as boopd_worker, and an owner pool to read the projections.
func storeFixture(t *testing.T) (*activities.Activities, *pgxpool.Pool) {
	t.Helper()
	srv := storetest.Start(t)
	if _, err := store.Migrate(t.Context(), srv.OwnerDSN(), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	open := func(dsn string) *pgxpool.Pool {
		p, err := store.Open(t.Context(), dsn)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(p.Close)
		return p
	}
	worker := open(srv.RoleDSN(storetest.RoleWorker))
	a := activities.New(&activities.Deps{Config: &config.Config{}, Store: store.New(worker, storetest.RoleWorker, nil)})
	return a, open(srv.OwnerDSN())
}

func count(t *testing.T, p *pgxpool.Pool, q string) int {
	t.Helper()
	var n int
	if err := p.QueryRow(t.Context(), q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestRecordRun_LeavesRunsUpdatesAndDependencies(t *testing.T) {
	a, owner := storeFixture(t)
	start := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	in := &workflows.RecordRunInput{
		RunID: "exec-1:1", RepoID: 42, InstallationID: 7, Slug: "acme/api", CauseKey: "wf:repo/github/42:exec-1:0",
		QueuedAt: start.Add(-3 * time.Second), StartedAt: start, FinishedAt: start.Add(2 * time.Minute),
		Outcome: workflows.OutcomeSucceeded,
		Result: &workflows.RunResult{
			Outcome: workflows.OutcomeSucceeded, RepositoryResult: "done", RenovateVersion: "44.179.2",
			Tuples: []workflows.UpdateTuple{{
				BranchName: "renovate/x", PRNumber: 5, Manager: "gomod", Datasource: "go", DepName: "golang.org/x/crypto",
				PackageFile: "go.mod", UpdateType: "minor", CurrentVersion: "v0.57.0", NewVersion: "v0.58.0",
			}},
			Dependencies: []workflows.Dependency{
				{
					Manager:        "gomod",
					PackageFile:    "go.mod",
					DepName:        "golang.org/x/crypto",
					PackageName:    "golang.org/x/crypto",
					Datasource:     "go",
					CurrentValue:   "v0.57.0",
					CurrentVersion: "v0.57.0",
				},
				{
					Manager:        "npm",
					PackageFile:    "package.json",
					DepName:        "@types/node",
					PackageName:    "@types/node",
					Datasource:     "npm",
					CurrentValue:   "^22.0.0",
					CurrentVersion: "22.1.0",
				},
			},
			RateBefore: &workflows.Readings{Resources: map[string]workflows.Reading{workflows.ResourceCore: {Remaining: 5000}}},
			RateAfter:  &workflows.Readings{Resources: map[string]workflows.Reading{workflows.ResourceCore: {Remaining: 4900}}},
		},
	}
	for range 2 { // a retry is a no-op
		if err := a.RecordRun(t.Context(), in); err != nil {
			t.Fatalf("RecordRun: %v", err)
		}
	}

	if n := count(t, owner, `SELECT count(*) FROM runs WHERE id = 'exec-1:1' AND outcome = 'Succeeded'
		AND queued_ms = 3000 AND duration_ms = 120000 AND cause_key = 'wf:repo/github/42:exec-1:0'
		AND spend->'core'->>'after' = '4900'`); n != 1 {
		t.Errorf("runs rows = %d, want 1", n)
	}
	if n := count(t, owner, `SELECT count(*) FROM branch_updates WHERE repo_id = 42 AND pr_number = 5
		AND purl_from = 'pkg:golang/golang.org/x/crypto@v0.57.0' AND purl_to = 'pkg:golang/golang.org/x/crypto@v0.58.0'`); n != 1 {
		t.Errorf("branch_updates rows = %d, want 1", n)
	}
	if n := count(t, owner, `SELECT count(*) FROM dependencies WHERE repo_id = 42 AND purl IN
		('pkg:golang/golang.org/x/crypto@v0.57.0', 'pkg:npm/%40types/node@22.1.0')`); n != 2 {
		t.Errorf("dependencies rows = %d, want 2", n)
	}
	if n := count(t, owner, `SELECT count(*) FROM repositories WHERE id = 42 AND slug = 'acme/api' AND owner = 'acme'
		AND state = 'idle' AND last_run_id = 'exec-1:1'`); n != 1 {
		t.Errorf("repositories rows = %d, want 1", n)
	}
	if n := count(t, owner, `SELECT count(*) FROM events WHERE key LIKE 'run:exec-1:1:%'`); n != 2 {
		t.Errorf("events = %d, want started and finished", n)
	}
}

func TestRecordRun_TimeoutHasNoReport(t *testing.T) {
	a, owner := storeFixture(t)
	start := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	if err := a.RecordRun(t.Context(), &workflows.RecordRunInput{
		RunID: "exec-2:1", RepoID: 43, Slug: "acme/web", QueuedAt: start, StartedAt: start, FinishedAt: start.Add(58 * time.Minute),
		Outcome: workflows.OutcomeTimedOut,
	}); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	if n := count(t, owner, `SELECT count(*) FROM repositories WHERE id = 43 AND state = 'errored'`); n != 1 {
		t.Errorf("errored repositories = %d, want 1", n)
	}
}

func TestRecordRun_StoreDisabled(t *testing.T) {
	t.Parallel()
	a := activities.New(&activities.Deps{Config: &config.Config{}})
	if err := a.RecordRun(t.Context(), &workflows.RecordRunInput{RunID: "x"}); err != nil {
		t.Errorf("RecordRun with no store = %v, want nil", err)
	}
}

func TestRecordEvents_EveryWorkflowKind(t *testing.T) {
	a, owner := storeFixture(t)
	at := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	ev := func(seq int, kind string, d time.Duration) workflows.RecordedEvent {
		return workflows.RecordedEvent{
			Key: fmt.Sprintf("wf:repo/github/50:exec:%d", seq), Kind: kind, OccurredAt: at.Add(d), InstallationID: 7, RepoID: 50,
		}
	}
	onboarded := ev(0, workflows.KindRepoOnboarded, 0)
	onboarded.Repository = &workflows.RecordRepository{Slug: "acme/api", Profile: "default", Via: workflows.ViaDiscovery}
	scheduled := ev(1, workflows.KindRepoRunScheduled, time.Minute)
	scheduled.Scheduled = &workflows.RecordScheduled{NextDue: at.Add(time.Hour), Priority: workflows.PriorityNormal}
	recheck := ev(2, workflows.KindActionRecheck, 2*time.Minute)
	recheck.Actor = &workflows.RecordActor{Login: "dg@example.com", Type: "oidc"}
	recheck.Recheck = &workflows.RecordRecheck{RequestID: "req-1"}
	wait := ev(3, workflows.KindRunBudgetWait, 3*time.Minute)
	wait.State = &workflows.RecordState{Until: at.Add(10 * time.Minute)}
	refreshed := ev(4, workflows.KindRepoRefreshed, 4*time.Minute)
	refreshed.Repository = &workflows.RecordRepository{Slug: "acme/api-v2", Profile: "python"}
	pass := workflows.RecordedEvent{
		Key: "wf:discovery/main:exec:0", Kind: workflows.KindDiscoveryFinished, OccurredAt: at,
		Discovery: &workflows.RecordDiscovery{App: "main", StartedAt: at.Add(-time.Minute), FinishedAt: at, Seen: 3, Onboarded: 1},
	}
	found := workflows.RecordedEvent{
		Key: "wf:discovery/main:exec:1", Kind: workflows.KindInstallationAdded, OccurredAt: at, InstallationID: 7,
		Installation: &workflows.RecordInstallation{App: "main", Account: "acme"},
	}
	in := &workflows.RecordEventsInput{Events: []workflows.RecordedEvent{onboarded, scheduled, recheck, wait, refreshed, pass, found}}
	for range 2 {
		if err := a.RecordEvents(t.Context(), in); err != nil {
			t.Fatalf("RecordEvents: %v", err)
		}
	}
	if n := count(t, owner, `SELECT count(*) FROM repositories WHERE id = 50 AND slug = 'acme/api-v2' AND profile = 'python'
		AND onboarded_at IS NOT NULL AND next_due = '2026-11-21T10:00:00Z' AND state = 'rate-limited'`); n != 1 {
		t.Errorf("repositories rows = %d, want 1", n)
	}
	if n := count(t, owner, `SELECT count(*) FROM events WHERE kind = 'action.recheck' AND actor_login = 'dg@example.com'
		AND data->>'request_id' = 'req-1'`); n != 1 {
		t.Errorf("recheck events = %d, want 1", n)
	}
	if n := count(t, owner, `SELECT count(*) FROM discovery_passes WHERE app = 'main' AND seen = 3`); n != 1 {
		t.Errorf("discovery_passes = %d, want 1", n)
	}
	if n := count(t, owner, `SELECT count(*) FROM installations WHERE id = 7 AND app = 'main' AND account_login = 'acme'
		AND NOT suspended`); n != 1 {
		t.Errorf("installations = %d, want 1", n)
	}
	if n := count(t, owner, `SELECT count(*) FROM events`); n != len(in.Events) {
		t.Errorf("events = %d, want %d", n, len(in.Events))
	}
}
