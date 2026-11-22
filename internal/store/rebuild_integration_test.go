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
	"bufio"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/storetest"
)

// logLine is one event of testdata/log.jsonl.
type logLine struct {
	Key            string       `json:"key"`
	OccurredAt     time.Time    `json:"occurred_at"`
	Kind           store.Kind   `json:"kind"`
	Source         store.Source `json:"source"`
	InstallationID int64        `json:"installation_id"`
	RepoID         int64        `json:"repo_id"`
	PRNumber       int          `json:"pr_number"`
	Actor          struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"actor"`
	CauseKey string          `json:"cause_key"`
	Summary  string          `json:"summary"`
	Data     json.RawMessage `json:"data"`
}

// recordedLog reads testdata/log.jsonl, in file order: some events
// arrive after later ones, as deliveries and retries do.
func recordedLog(t *testing.T) []store.Event {
	t.Helper()
	f, err := os.Open("testdata/log.jsonl")
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()
	var events []store.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var l logLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("decode %s: %v", sc.Text(), err)
		}
		data, err := store.DecodeData(l.Kind, l.Data)
		if err != nil {
			t.Fatalf("decode %s: %v", l.Key, err)
		}
		events = append(events, store.Event{
			Key: l.Key, OccurredAt: l.OccurredAt, Kind: l.Kind, Source: l.Source,
			InstallationID: l.InstallationID, RepoID: l.RepoID, PRNumber: l.PRNumber,
			Actor: store.Actor{Login: l.Actor.Login, Type: l.Actor.Type}, CauseKey: l.CauseKey,
			Summary: l.Summary, Data: data,
		})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read log: %v", err)
	}
	return events
}

var projections = []string{
	"installations", "repositories", "runs", "branch_updates", "dependencies",
	"pull_requests", "dashboard_items", "webhook_deliveries", "discovery_passes", "dashboards",
}

// projectionState snapshots every projection.
func projectionState(t *testing.T, p *pgxpool.Pool) map[string]string {
	t.Helper()
	out := make(map[string]string, len(projections))
	for _, table := range projections {
		out[table] = snapshot(t, p, table, "true")
	}
	return out
}

func TestRebuild_EqualsIncremental(t *testing.T) {
	srv := migrated(t)
	owner := pool(t, srv.OwnerDSN())
	// Each event goes in as the role that writes its source.
	worker := store.New(pool(t, srv.RoleDSN(storetest.RoleWorker)), storetest.RoleWorker, nil)
	ingest := store.New(pool(t, srv.RoleDSN(storetest.RoleIngest)), storetest.RoleIngest, nil)

	events := recordedLog(t)
	covered := make(map[store.Kind]bool)
	for i := range events {
		covered[events[i].Kind] = true
		writer := worker
		if events[i].Source == store.SourceWebhook {
			writer = ingest
		}
		if err := writer.Append(t.Context(), events[i]); err != nil {
			t.Fatalf("Append(%s): %v", events[i].Key, err)
		}
	}
	for _, k := range store.Kinds() {
		if !covered[k] {
			t.Errorf("log has no %s event", k)
		}
	}
	incremental := projectionState(t, owner)
	if incremental["runs"] == "[]" || incremental["dashboard_items"] == "[]" {
		t.Fatalf("incremental projections are empty: %v", incremental)
	}

	// A small batch makes Rebuild page.
	res, err := store.Rebuild(t.Context(), owner, 4, quiet)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if res.Events != len(events) || res.Unknown != 0 {
		t.Errorf("Rebuild = %+v, want %d events", res, len(events))
	}
	rebuilt := projectionState(t, owner)
	for _, table := range projections {
		if rebuilt[table] != incremental[table] {
			t.Errorf("%s differs:\nincremental %s\n    rebuilt %s", table, incremental[table], rebuilt[table])
		}
	}
}
