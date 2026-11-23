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

package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/storetest"
	"github.com/donaldgifford/boop/test/fakegithub"
)

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

type parseCounter struct{ outcomes []string }

func (p *parseCounter) DashboardParse(o string) { p.outcomes = append(p.outcomes, o) }

// storeHarness is the handler with a StoreSink writing as boopd_ingest,
// and an owner pool to read the projections.
type storeHarness struct {
	h      *Handler
	owner  *pgxpool.Pool
	parses *parseCounter
	n      int
	clock  time.Time
}

func newStoreHarness(t *testing.T) *storeHarness {
	t.Helper()
	srv := storetest.Start(t)
	if _, err := store.Migrate(t.Context(), srv.OwnerDSN(), quietLog); err != nil {
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
	sh := &storeHarness{owner: open(srv.OwnerDSN()), parses: &parseCounter{}, clock: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	sink := &StoreSink{Store: store.New(open(srv.RoleDSN(storetest.RoleIngest)), storetest.RoleIngest, nil), Logger: quietLog, Metrics: sh.parses}
	sh.h = NewHandler(&Options{
		Config: testConfig(), Temporal: &fakeTemporal{errs: map[Kind]error{}}, TaskQueue: "boopd", Sink: sink,
		Now: func() time.Time { sh.clock = sh.clock.Add(time.Second); return sh.clock },
	})
	return sh
}

// deliver posts a fixture with a fresh delivery GUID and returns the
// status and the GUID.
func (sh *storeHarness) deliver(t *testing.T, event, name string) (int, string) {
	t.Helper()
	sh.n++
	guid := fmt.Sprintf("6a0c1f5e-0000-4000-8000-%012d", sh.n)
	body := payload(t, name)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderEvent, event)
	req.Header.Set(HeaderDelivery, guid)
	req.Header.Set(HeaderTargetID, "1")
	req.Header.Set(HeaderSignature, fakegithub.Sign(testSecret, body))
	w := httptest.NewRecorder()
	sh.h.ServeHTTP(w, req)
	return w.Code, guid
}

func (sh *storeHarness) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := sh.owner.QueryRow(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestStoreSink_EachDerivedKind(t *testing.T) {
	sh := newStoreHarness(t)
	tests := []struct {
		event, fixture string
		kinds          []store.Kind
		check          string // a projection query that must count 1
	}{
		{"pull_request", "pull-request-opened", []store.Kind{store.KindPROpened}, "SELECT count(*) FROM pull_requests WHERE state = 'open'"},
		{"pull_request", "pull-request-closed", []store.Kind{store.KindPRClosed}, "SELECT count(*) FROM pull_requests WHERE state = 'closed'"},
		{"pull_request", "pull-request-closed-human", nil, ""},
		{
			"issues",
			"issues-edited-own",
			[]store.Kind{store.KindDashboardUpdated},
			"SELECT count(*) FROM dashboard_items WHERE marker = 'approve-branch' AND branch = 'renovate/x'",
		},
		{
			"issues", "issues-edited-dashboard",
			[]store.Kind{store.KindDashboardTicked},
			"SELECT count(*) FROM dashboard_items WHERE ticked_by = 'alice'",
		},
		{"issues", "issues-edited-human-issue", nil, ""},
		{
			"installation",
			"installation-created",
			[]store.Kind{store.KindInstallationAdded},
			"SELECT count(*) FROM installations WHERE account_login = 'acme'",
		},
		{"installation", "installation-suspend", []store.Kind{store.KindInstallationSusp}, "SELECT count(*) FROM installations WHERE suspended"},
		{
			"installation",
			"installation-unsuspend",
			[]store.Kind{store.KindInstallationUnsusp},
			"SELECT count(*) FROM installations WHERE NOT suspended",
		},
		{
			"installation",
			"installation-deleted",
			[]store.Kind{store.KindInstallationRemove},
			"SELECT count(*) FROM installations WHERE removed_at IS NOT NULL",
		},
		{"push", "push-config-added", nil, ""},
		{"repository", "repository-renamed", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			code, guid := sh.deliver(t, tt.event, tt.fixture)
			if code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", code)
			}
			if n := sh.count(
				t,
				"SELECT count(*) FROM webhook_deliveries WHERE delivery_id = $1 AND received_at IS NOT NULL AND payload IS NOT NULL",
				guid,
			); n != 1 {
				t.Errorf("webhook_deliveries rows = %d, want 1", n)
			}
			if n := sh.count(
				t,
				"SELECT count(*) FROM events WHERE key LIKE 'delivery:' || $1 || ':%' AND kind <> 'delivery.received'",
				guid,
			); n != len(
				tt.kinds,
			) {
				t.Errorf("derived events = %d, want %v", n, tt.kinds)
			}
			for _, k := range tt.kinds {
				if n := sh.count(t, "SELECT count(*) FROM events WHERE key LIKE 'delivery:' || $1 || ':%' AND kind = $2", guid, string(k)); n != 1 {
					t.Errorf("%s events = %d, want 1", k, n)
				}
			}
			if tt.check != "" {
				if n := sh.count(t, tt.check); n != 1 {
					t.Errorf("%s = %d, want 1", tt.check, n)
				}
			}
		})
	}
	if len(sh.parses.outcomes) == 0 {
		t.Error("no dashboard parse was counted")
	}
}

// failingStore is a store that is down.
type failingStore struct{ calls int }

func (f *failingStore) Append(context.Context, ...store.Event) error {
	f.calls++
	return errors.New("connection refused")
}

func TestStoreSink_OutageStillAnswers(t *testing.T) {
	t.Parallel()
	down := &failingStore{}
	h := NewHandler(&Options{
		Config: testConfig(), Temporal: &fakeTemporal{errs: map[Kind]error{}}, TaskQueue: "boopd",
		Sink: &StoreSink{Store: down, Logger: quietLog},
	})
	body := payload(t, "pull-request-opened")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderEvent, "pull_request")
	req.Header.Set(HeaderDelivery, "6a0c1f5e-0000-4000-8000-000000000099")
	req.Header.Set(HeaderTargetID, "1")
	req.Header.Set(HeaderSignature, fakegithub.Sign(testSecret, body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted || down.calls != 1 {
		t.Errorf("status %d after %d appends, want 202 after one failed append", w.Code, down.calls)
	}
}
