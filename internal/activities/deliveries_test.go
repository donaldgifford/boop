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
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/workflows"
	"github.com/donaldgifford/boop/test/fakegithub"
)

// auditHarness runs ListHookDeliveries against the fake's deliveries to
// a receiver whose status the test sets.
type auditHarness struct {
	gh     *fakegithub.Server
	url    string
	status atomic.Int32
	env    *testsuite.TestActivityEnvironment
}

func newAudit(t *testing.T, now func() time.Time) *auditHarness {
	t.Helper()
	h := &auditHarness{gh: fakegithub.New(t)}
	h.status.Store(http.StatusAccepted)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(int(h.status.Load())) }))
	t.Cleanup(recv.Close)
	h.url = recv.URL
	cfg := &config.Config{Apps: []*config.App{{Name: "main", AppID: 7, Endpoint: h.gh.BaseURL(), PrivateKey: config.NewSecret(h.gh.PEM)}}}
	h.env = (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	activities.New(&activities.Deps{Config: cfg, Now: now}).Register(h.env)
	return h
}

func (h *auditHarness) deliver(t *testing.T, event string, status int, opts ...fakegithub.DeliverOption) fakegithub.Delivery {
	t.Helper()
	h.status.Store(int32(status))
	return h.gh.Deliver(t, h.url, event, map[string]any{"action": "x"}, opts...)
}

func (h *auditHarness) audit(t *testing.T, after int64) *workflows.DeliveryAudit {
	t.Helper()
	val, err := h.env.ExecuteActivity(workflows.ListHookDeliveriesActivity, &workflows.ListHookDeliveriesInput{App: "main", After: after})
	if err != nil {
		t.Fatalf("ListHookDeliveries: %v", err)
	}
	var out workflows.DeliveryAudit
	if err := val.Get(&out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func TestListHookDeliveries_Classifies(t *testing.T) {
	t.Parallel()
	h := newAudit(t, nil)
	h.deliver(t, "push", http.StatusAccepted)
	h.deliver(t, "push", http.StatusServiceUnavailable) // failed, stays failed
	lost := h.deliver(t, "issues", http.StatusServiceUnavailable)
	h.deliver(t, "issues", http.StatusAccepted, fakegithub.WithGUID(lost.GUID)) // redelivered by hand
	h.gh.RecordUndelivered(t, "pull_request", "closed")                         // GitHub could not connect

	got := h.audit(t, 0)
	want := []workflows.DeliveryCounts{
		{Event: "issues", Redelivered: 1},
		{Event: "pull_request", Failed: 1},
		{Event: "push", Failed: 1, OK: 1},
	}
	if !slices.Equal(got.Counts, want) {
		t.Errorf("counts = %+v, want %+v", got.Counts, want)
	}
	if got.Newest != 5 || got.Read != 5 {
		t.Errorf("newest %d read %d, want 5 and 5", got.Newest, got.Read)
	}
}

// The cursor bounds the next pass; a pass with a lost cursor reads the
// same window and gives the same answer.
func TestListHookDeliveries_Cursor(t *testing.T) {
	t.Parallel()
	h := newAudit(t, nil)
	for range 150 {
		h.deliver(t, "push", http.StatusAccepted)
	}
	first := h.audit(t, 0)
	if first.Newest != 150 || first.Read != 150 || first.Pages != 2 {
		t.Fatalf("first pass = %+v, want 150 read over 2 pages", *first)
	}
	h.deliver(t, "push", http.StatusServiceUnavailable)
	next := h.audit(t, first.Newest)
	if next.Newest != 151 || next.Read != 1 || next.Pages != 1 ||
		!slices.Equal(next.Counts, []workflows.DeliveryCounts{{Event: "push", Failed: 1}}) {
		t.Errorf("next pass = %+v, want only the new failed delivery from one page", *next)
	}
	again, lostAgain := h.audit(t, 0), h.audit(t, 0)
	if !slices.Equal(again.Counts, lostAgain.Counts) || again.Newest != lostAgain.Newest || again.Read != 151 {
		t.Errorf("lost-cursor passes differ: %+v vs %+v", *again, *lostAgain)
	}
}

// Deliveries older than GitHub's 3-day window are not read.
func TestListHookDeliveries_Window(t *testing.T) {
	t.Parallel()
	h := newAudit(t, func() time.Time { return time.Now().Add(4 * 24 * time.Hour) })
	h.deliver(t, "push", http.StatusServiceUnavailable)
	if got := h.audit(t, 0); got.Read != 0 || len(got.Counts) != 0 || got.Newest != 0 {
		t.Errorf("pass = %+v, want nothing read past the window", *got)
	}
}
