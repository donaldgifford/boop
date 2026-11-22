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
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/storetest"
)

// The design's defaults (DESIGN-0004 § Retention).
var retention = store.Retention{
	Events:           400 * 24 * time.Hour,
	DeliveryPayloads: 30 * 24 * time.Hour,
	Offboarded:       90 * 24 * time.Hour,
}

// maintenanceNow is the clock override: every fixture is placed
// relative to it, not to the database's clock.
var maintenanceNow = time.Date(2027, 1, 15, 12, 0, 0, 0, time.UTC)

func ago(days int) time.Time { return maintenanceNow.Add(-time.Duration(days) * 24 * time.Hour) }

func exec(t *testing.T, p *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := p.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// maintenanceFixture is a migrated store with an owner pool for the
// fixtures and a Store as boopd_worker, which runs maintenance.
func maintenanceFixture(t *testing.T) (owner *pgxpool.Pool, worker *store.Store) {
	t.Helper()
	srv := migrated(t)
	owner = pool(t, srv.OwnerDSN())
	return owner, store.New(pool(t, srv.RoleDSN(storetest.RoleWorker)), storetest.RoleWorker, nil)
}

func maintain(t *testing.T, s *store.Store, rule store.MaintenanceRule) int64 {
	t.Helper()
	n, err := s.Maintain(t.Context(), rule, maintenanceNow, retention)
	if err != nil {
		t.Fatalf("Maintain(%s): %v", rule, err)
	}
	return n
}

// Partitions: a month wholly past 400 days is dropped, the month the
// cutoff falls in stays with its keys, the default partition loses only
// the rows before that month, and the months ahead exist.
func TestMaintain_Partitions(t *testing.T) {
	owner, worker := maintenanceFixture(t)
	exec(t, owner, "CREATE TABLE events_202410 PARTITION OF events FOR VALUES FROM ('2024-10-01') TO ('2024-11-01')")
	exec(t, owner, "CREATE TABLE events_202512 PARTITION OF events FOR VALUES FROM ('2025-12-01') TO ('2026-01-01')")
	for i, at := range []time.Time{
		time.Date(2024, 10, 10, 0, 0, 0, 0, time.UTC), // dropped with its partition
		time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC),  // its month ends after the cutoff: kept
		ago(430), // default partition, before the cutoff's month: deleted
		ago(390), // default partition, inside: kept
	} {
		exec(t, owner, "INSERT INTO events (key, occurred_at, kind, source) VALUES ($1, $2, 'action.recheck', 'api')",
			"k"+string(rune('a'+i)), at)
		exec(t, owner, "INSERT INTO event_keys (key, occurred_at) VALUES ($1, $2)", "k"+string(rune('a'+i)), at)
	}

	if n := maintain(t, worker, store.RulePartitions); n != 1 {
		t.Errorf("dropped %d partitions, want 1", n)
	}
	if n := count(t, owner, "SELECT count(*) FROM pg_class WHERE relname = 'events_202410'"); n != 0 {
		t.Error("events_202410 was not dropped")
	}
	if n := count(t, owner, "SELECT count(*) FROM events"); n != 2 {
		t.Errorf("events left = %d, want the 2 inside the window", n)
	}
	if n := count(t, owner, "SELECT count(*) FROM event_keys"); n != 2 {
		t.Errorf("event keys left = %d, want 2", n)
	}
	ahead := time.Now().UTC().AddDate(0, store.PartitionsAhead, 0).Format("200601")
	if n := count(t, owner, "SELECT count(*) FROM pg_class WHERE relname = $1", "events_"+ahead); n != 1 {
		t.Errorf("partition events_%s missing", ahead)
	}
}

// Delivery payloads past 30 days are nulled; the rows stay.
func TestMaintain_DeliveryPayloads(t *testing.T) {
	owner, worker := maintenanceFixture(t)
	for i, days := range []int{31, 29} {
		exec(t, owner, "INSERT INTO webhook_deliveries (delivery_id, received_at, payload) VALUES ($1, $2, '{\"a\":1}')",
			uuid(i+1), ago(days))
	}
	if n := maintain(t, worker, store.RuleDeliveryPayloads); n != 1 {
		t.Errorf("nulled %d payloads, want 1", n)
	}
	if n := count(t, owner, "SELECT count(*) FROM webhook_deliveries WHERE payload IS NULL AND received_at < $1", ago(30)); n != 1 {
		t.Error("the old payload was not nulled")
	}
	if n := count(t, owner, "SELECT count(*) FROM webhook_deliveries WHERE payload IS NOT NULL"); n != 1 {
		t.Error("the recent payload did not stay")
	}
	if n := count(t, owner, "SELECT count(*) FROM webhook_deliveries"); n != 2 {
		t.Errorf("deliveries = %d, want both rows kept", n)
	}
}

// An offboarded repository's projections go 90 days after it left; one
// offboarded more recently, and an active one, stay whole.
func TestMaintain_Offboarded(t *testing.T) {
	owner, worker := maintenanceFixture(t)
	repos := map[int64]*time.Time{1: new(ago(91)), 2: new(ago(89)), 3: nil}
	for id, off := range repos {
		exec(
			t,
			owner,
			"INSERT INTO repositories (id, installation_id, owner, slug, offboarded_at, updated_at) VALUES ($1, 7, 'acme', 'acme/r', $2, $3)",
			id,
			off,
			ago(100),
		)
		exec(t, owner, "INSERT INTO runs (id, repo_id, started_at, updated_at) VALUES ($1, $2, $3, $3)", "run-"+string(rune('0'+id)), id, ago(100))
		exec(t, owner, "INSERT INTO branch_updates (repo_id, branch, package_file, package_name, run_id) VALUES ($1, 'b', 'go.mod', 'x', 'r')", id)
		exec(t, owner, "INSERT INTO dependencies (repo_id, manager, package_file, package_name, run_id) VALUES ($1, 'gomod', 'go.mod', 'x', 'r')", id)
		exec(t, owner, "INSERT INTO pull_requests (repo_id, number, state, gh_updated_at) VALUES ($1, 1, 'open', $2)", id, ago(100))
		exec(
			t,
			owner,
			"INSERT INTO dashboard_items (repo_id, marker, branch, first_seen_at, last_seen_at) VALUES ($1, 'm', 'b', $2, $2)",
			id,
			ago(100),
		)
		exec(t, owner, "INSERT INTO dashboards (repo_id, updated_at) VALUES ($1, $2)", id, ago(100))
	}
	if n := maintain(t, worker, store.RuleOffboarded); n != 7 {
		t.Errorf("deleted %d rows, want the 7 of repository 1", n)
	}
	for _, table := range []string{"runs", "branch_updates", "dependencies", "pull_requests", "dashboard_items", "dashboards"} {
		if n := count(t, owner, "SELECT count(*) FROM "+table+" WHERE repo_id = 1"); n != 0 {
			t.Errorf("%s keeps %d rows of repository 1", table, n)
		}
		if n := count(t, owner, "SELECT count(*) FROM "+table+" WHERE repo_id IN (2, 3)"); n != 2 {
			t.Errorf("%s has %d rows of repositories 2 and 3, want 2", table, n)
		}
	}
	if n := count(t, owner, "SELECT count(*) FROM repositories"); n != 2 {
		t.Errorf("repositories = %d, want 2", n)
	}
}

// Runs that started 400 days ago go; newer ones stay.
func TestMaintain_Runs(t *testing.T) {
	owner, worker := maintenanceFixture(t)
	for i, days := range []int{401, 399} {
		exec(t, owner, "INSERT INTO runs (id, repo_id, started_at, updated_at) VALUES ($1, 1, $2, $2)", "run-"+string(rune('a'+i)), ago(days))
	}
	if n := maintain(t, worker, store.RuleRuns); n != 1 {
		t.Errorf("deleted %d runs, want 1", n)
	}
	if n := count(t, owner, "SELECT count(*) FROM runs WHERE id = 'run-b'"); n != 1 {
		t.Error("the run inside the window was deleted")
	}
}

// A zero retention keeps everything.
func TestMaintain_ZeroRetentionKeeps(t *testing.T) {
	owner, worker := maintenanceFixture(t)
	exec(t, owner, "INSERT INTO runs (id, repo_id, started_at, updated_at) VALUES ('old', 1, '2000-01-01', '2000-01-01')")
	for _, rule := range store.MaintenanceRules() {
		if n, err := worker.Maintain(t.Context(), rule, maintenanceNow, store.Retention{}); err != nil || n != 0 {
			t.Errorf("Maintain(%s) with no retention = %d, %v; want 0, nil", rule, n, err)
		}
	}
	if n := count(t, owner, "SELECT count(*) FROM runs"); n != 1 {
		t.Error("a run was deleted with no retention")
	}
}
