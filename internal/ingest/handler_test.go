package ingest

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/boop/internal/workflows"
	"github.com/donaldgifford/boop/test/fakegithub"
)

// fakeTemporal records calls and fails them as configured.
type fakeTemporal struct {
	mu    sync.Mutex
	calls []string // kind workflowID
	opts  []client.StartWorkflowOptions
	errs  map[Kind]error
	block bool // wait for the context to end
}

func (f *fakeTemporal) call(ctx context.Context, kind Kind, id string, opts *client.StartWorkflowOptions) error {
	f.mu.Lock()
	f.calls = append(f.calls, string(kind)+" "+id)
	if opts != nil {
		f.opts = append(f.opts, *opts)
	}
	err, block := f.errs[kind], f.block
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	return err
}

func (f *fakeTemporal) SignalWorkflow(ctx context.Context, id, _, _ string, _ any) error {
	return f.call(ctx, KindSignal, id, nil)
}

//nolint:gocritic // hugeParam: the Temporal interface passes options by value
func (f *fakeTemporal) SignalWithStartWorkflow(ctx context.Context, id, _ string, _ any,
	opts client.StartWorkflowOptions, _ any, _ ...any,
) (client.WorkflowRun, error) {
	return nil, f.call(ctx, KindSignalWithStart, id, &opts)
}

//nolint:gocritic // hugeParam: the Temporal interface passes options by value
func (f *fakeTemporal) ExecuteWorkflow(ctx context.Context, opts client.StartWorkflowOptions, _ any, _ ...any) (client.WorkflowRun, error) {
	return nil, f.call(ctx, KindStartDiscovery, opts.ID, &opts)
}

// recordSink keeps every record.
type recordSink struct {
	mu   sync.Mutex
	recs []*Record
}

func (s *recordSink) Record(_ context.Context, r *Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, r)
}

type harness struct {
	h        *Handler
	temporal *fakeTemporal
	sink     *recordSink
}

func newHarness(deadline time.Duration) *harness {
	hs := &harness{temporal: &fakeTemporal{errs: map[Kind]error{}}, sink: &recordSink{}}
	hs.h = NewHandler(&Options{
		Config: testConfig(), Temporal: hs.temporal, TaskQueue: "boopd",
		Sink: hs.sink, Deadline: deadline,
	})
	return hs
}

// deliver posts body as event, signed with secret, for app id target.
func (hs *harness) deliver(t *testing.T, event, target, secret string, body []byte) int {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderEvent, event)
	req.Header.Set(HeaderDelivery, "d-1")
	req.Header.Set(HeaderTargetID, target)
	req.Header.Set(HeaderSignature, fakegithub.Sign(secret, body))
	w := httptest.NewRecorder()
	hs.h.ServeHTTP(w, req)
	return w.Code
}

// only is the one record the sink got.
func (hs *harness) only(t *testing.T) *Record {
	t.Helper()
	hs.sink.mu.Lock()
	defer hs.sink.mu.Unlock()
	if len(hs.sink.recs) != 1 {
		t.Fatalf("sink got %d records, want 1", len(hs.sink.recs))
	}
	return hs.sink.recs[0]
}

func TestHandler_Answers(t *testing.T) {
	t.Parallel()
	notFound := serviceerror.NewNotFound("workflow not found")
	tests := []struct {
		name    string
		event   string
		file    string
		target  string
		secret  string
		errs    map[Kind]error
		status  int
		outcome Outcome
		calls   int
	}{
		{name: "routed", event: "issues", file: "issues-edited-dashboard", status: 202, outcome: OutcomeRouted, calls: 1},
		{name: "ping", event: "ping", file: "ping", status: 200, outcome: OutcomeIgnored},
		{name: "own bot", event: "issues", file: "issues-edited-own", status: 202, outcome: OutcomeOwn},
		{name: "nothing to do", event: "push", file: "push-other-files", status: 202, outcome: OutcomeIgnored},
		{name: "unknown event", event: "not_an_event", file: "ping", status: 202, outcome: OutcomeIgnored},
		{
			name:    "not onboarded",
			event:   "issues",
			file:    "issues-edited-dashboard",
			errs:    map[Kind]error{KindSignal: notFound},
			status:  202,
			outcome: OutcomeIgnored,
			calls:   1,
		},
		{
			name:    "temporal down",
			event:   "issues",
			file:    "issues-edited-dashboard",
			errs:    map[Kind]error{KindSignal: errors.New("unavailable")},
			status:  503,
			outcome: OutcomeFailed,
			calls:   1,
		},
		{
			name: "one of two failed", event: "installation", file: "installation-unsuspend",
			errs: map[Kind]error{KindStartDiscovery: errors.New("unavailable")}, status: 503, outcome: OutcomeFailed, calls: 2,
		},
		{name: "wrong secret", event: "issues", file: "issues-edited-dashboard", secret: "not it", status: 401, outcome: OutcomeRejected},
		{name: "unknown app", event: "issues", file: "issues-edited-dashboard", target: "99", status: 400, outcome: OutcomeRejected},
		{name: "app without webhook", event: "issues", file: "issues-edited-dashboard", target: "2", status: 400, outcome: OutcomeRejected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hs := newHarness(0)
			if tt.errs != nil {
				hs.temporal.errs = tt.errs
			}
			target, secret := tt.target, tt.secret
			if target == "" {
				target = "1"
			}
			if secret == "" {
				secret = testSecret
			}
			if got := hs.deliver(t, tt.event, target, secret, payload(t, tt.file)); got != tt.status {
				t.Errorf("status = %d, want %d", got, tt.status)
			}
			rec := hs.only(t)
			if rec.Outcome != tt.outcome || rec.Status != tt.status || rec.DeliveryID != "d-1" {
				t.Errorf("record = %+v, want outcome %s status %d", rec, tt.outcome, tt.status)
			}
			if len(hs.temporal.calls) != tt.calls {
				t.Errorf("temporal calls = %v, want %d", hs.temporal.calls, tt.calls)
			}
			if tt.outcome == OutcomeRejected && (rec.SenderLogin != "" || rec.RepoID != 0) {
				t.Errorf("rejected record kept payload fields: %+v", rec)
			}
		})
	}
}

func TestHandler_RequestShape(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		method string
		path   string
		body   []byte
		status int
	}{
		{name: "wrong path", method: http.MethodPost, path: "/other", status: 404},
		{name: "not a post", method: http.MethodGet, path: "/webhooks/github", status: 405},
		{name: "too large", method: http.MethodPost, path: "/webhooks/github", body: bytes.Repeat([]byte("x"), MaxBody+1), status: 413},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hs := newHarness(0)
			w := httptest.NewRecorder()
			hs.h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, bytes.NewReader(tt.body)))
			if w.Code != tt.status {
				t.Errorf("status = %d, want %d", w.Code, tt.status)
			}
			if rec := hs.only(t); rec.Outcome != OutcomeRejected {
				t.Errorf("outcome = %s, want rejected", rec.Outcome)
			}
		})
	}
}

func TestHandler_Deadline(t *testing.T) {
	t.Parallel()
	hs := newHarness(50 * time.Millisecond)
	hs.temporal.block = true
	start := time.Now()
	if got := hs.deliver(t, "issues", "1", testSecret, payload(t, "issues-edited-dashboard")); got != 503 {
		t.Errorf("status = %d, want 503", got)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("answered after %s, want the 50ms deadline", d)
	}
	if rec := hs.only(t); !strings.Contains(rec.Actions[0].Error, "deadline") {
		t.Errorf("action error = %q, want a deadline", rec.Actions[0].Error)
	}
}

func TestHandler_StartOptions(t *testing.T) {
	t.Parallel()
	hs := newHarness(0)
	if got := hs.deliver(t, "push", "1", testSecret, payload(t, "push-config-added")); got != 202 {
		t.Fatalf("push status = %d", got)
	}
	if got := hs.deliver(t, "installation_repositories", "1", testSecret, payload(t, "installation-repos-added")); got != 202 {
		t.Fatalf("added status = %d", got)
	}
	want := []string{"signalWithStart repo/github/101", "startDiscovery discovery/boop-bot/installation/7"}
	if strings.Join(hs.temporal.calls, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v, want %v", hs.temporal.calls, want)
	}
	if o := hs.temporal.opts[0]; o.TaskQueue != "boopd" || o.ID != workflows.RepoWorkflowID(101) {
		t.Errorf("SignalWithStart options = %+v", o)
	}
	if o := hs.temporal.opts[1]; o.WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING {
		t.Errorf("discovery conflict policy = %v, want USE_EXISTING", o.WorkflowIDConflictPolicy)
	}
}
