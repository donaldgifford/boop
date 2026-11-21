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
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/storetest"
)

// recorder is a store.Metrics that keeps what it was told.
type recorder struct {
	mu       sync.Mutex
	outcomes map[string]int
}

func (r *recorder) StoreAppend(_, kind, outcome string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.outcomes == nil {
		r.outcomes = make(map[string]int)
	}
	r.outcomes[kind+"/"+outcome]++
}

func (*recorder) StoreAppendSeconds(string, time.Duration) {}

// pool opens a pool on dsn, closed with the test.
func pool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// count returns the rows of query.
func count(t *testing.T, p *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := p.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestAppend_DuplicateKeysAreSkipped(t *testing.T) {
	srv := migrated(t)
	var rec recorder
	s := store.New(pool(t, srv.RoleDSN(storetest.RoleWorker)), storetest.RoleWorker, &rec)
	at := time.Now().UTC().Truncate(time.Microsecond)
	e := store.Event{
		Key: "recheck/1", OccurredAt: at, Kind: store.KindActionRecheck, Source: store.SourceAPI,
		RepoID: 42, Actor: store.Actor{Login: "dg", Type: store.ActorOIDC}, Summary: "recheck",
		Data: &store.Recheck{V: 1, RequestID: "r1"},
	}

	if err := s.Append(t.Context(), e, e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Append(t.Context(), e); err != nil {
		t.Fatalf("second Append: %v", err)
	}

	owner := pool(t, srv.OwnerDSN())
	if n := count(t, owner, "SELECT count(*) FROM events WHERE key = $1", e.Key); n != 1 {
		t.Errorf("events with key = %d, want 1", n)
	}
	var data string
	if err := owner.QueryRow(t.Context(), "SELECT data->>'request_id' FROM events WHERE key = $1", e.Key).Scan(&data); err != nil || data != "r1" {
		t.Errorf("data.request_id = %q, %v; want r1", data, err)
	}
	if got := rec.outcomes["action.recheck/appended"]; got != 1 {
		t.Errorf("appended = %d, want 1", got)
	}
	if got := rec.outcomes["action.recheck/duplicate"]; got != 2 {
		t.Errorf("duplicate = %d, want 2", got)
	}
}

func TestAppend_IsAllOrNothing(t *testing.T) {
	srv := migrated(t)
	var rec recorder
	// boopd_ro may not insert: the failed key insert must leave nothing
	// behind and count as an error.
	s := store.New(pool(t, srv.RoleDSN(storetest.RoleRO)), storetest.RoleRO, &rec)
	at := time.Now().UTC()
	e := store.Event{Key: "recheck/2", OccurredAt: at, Kind: store.KindActionRecheck, Source: store.SourceAPI, Data: &store.Recheck{V: 1}}
	if err := s.Append(t.Context(), e); err == nil {
		t.Fatal("Append as boopd_ro succeeded, want an error")
	}
	if got := rec.outcomes["action.recheck/error"]; got != 1 {
		t.Errorf("error outcomes = %d, want 1", got)
	}
	if n := count(t, pool(t, srv.OwnerDSN()), "SELECT count(*) FROM event_keys"); n != 0 {
		t.Errorf("event_keys = %d, want 0", n)
	}
}
