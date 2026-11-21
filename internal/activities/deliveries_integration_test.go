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
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
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

// TestListHookDeliveries_Missed audits against the store: a GUID with a
// 2xx at GitHub and no delivery.received is missed, failed and
// redelivered ones are recorded too, and a re-read appends nothing new.
func TestListHookDeliveries_Missed(t *testing.T) {
	srv := storetest.Start(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := store.Migrate(t.Context(), srv.OwnerDSN(), quiet); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	open := func(dsn string) *store.Store {
		p, err := store.Open(t.Context(), dsn)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(p.Close)
		return store.New(p, "", nil)
	}
	ingest := open(srv.RoleDSN(storetest.RoleIngest))
	owner, err := store.Open(t.Context(), srv.OwnerDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)

	h := &auditHarness{gh: fakegithub.New(t)}
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(int(h.status.Load())) }))
	t.Cleanup(recv.Close)
	h.url = recv.URL
	cfg := &config.Config{Apps: []*config.App{{Name: "main", AppID: 7, Endpoint: h.gh.BaseURL(), PrivateKey: config.NewSecret(h.gh.PEM)}}}
	h.env = (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	activities.New(&activities.Deps{Config: cfg, Store: open(srv.RoleDSN(storetest.RoleWorker))}).Register(h.env)

	recorded := h.deliver(t, "push", http.StatusAccepted) // ingest recorded it: ok
	h.deliver(t, "push", http.StatusAccepted)             // no record: missed
	h.deliver(t, "push", http.StatusServiceUnavailable)   // failed
	lost := h.deliver(t, "issues", http.StatusServiceUnavailable)
	h.deliver(t, "issues", http.StatusAccepted, fakegithub.WithGUID(lost.GUID)) // redelivered
	for _, guid := range []string{recorded.GUID, lost.GUID} {
		if err := ingest.Append(t.Context(), store.Event{
			Key: "delivery:" + guid + ":received", OccurredAt: time.Now(), Kind: store.KindDeliveryReceived, Source: store.SourceWebhook,
			Data: &store.DeliveryReceived{V: 1, DeliveryID: guid, Attempt: 1, App: "main", Event: "push", Outcome: "routed"},
		}); err != nil {
			t.Fatalf("append received: %v", err)
		}
	}

	got := h.audit(t, 0)
	want := []workflows.DeliveryCounts{
		{Event: "issues", Redelivered: 1},
		{Event: "push", Failed: 1, Missed: 1, OK: 1},
	}
	if !slices.Equal(got.Counts, want) {
		t.Errorf("counts = %+v, want %+v", got.Counts, want)
	}
	audits := func() int { return count(t, owner, "SELECT count(*) FROM events WHERE source = 'audit'") }
	if n := audits(); n != 3 {
		t.Errorf("audit events = %d, want 3 (failed, missed, redelivered)", n)
	}
	if n := count(t, owner, "SELECT count(*) FROM webhook_deliveries WHERE missed AND github_status = 202 AND received_at IS NULL"); n != 1 {
		t.Errorf("missed rows = %d, want 1", n)
	}
	if n := count(t, owner, "SELECT count(*) FROM webhook_deliveries WHERE github_status = 503"); n != 1 {
		t.Errorf("failed rows = %d, want 1", n)
	}
	if n := count(t, owner,
		"SELECT count(*) FROM webhook_deliveries WHERE received_at IS NOT NULL AND github_status = 202 AND NOT missed"); n != 1 {
		t.Errorf("redelivered rows joined to ingest's = %d, want 1", n)
	}

	if again := h.audit(t, 0); !slices.Equal(again.Counts, want) {
		t.Errorf("re-read counts = %+v, want %+v", again.Counts, want)
	}
	if n := audits(); n != 3 {
		t.Errorf("audit events after a re-read = %d, want still 3", n)
	}
}
