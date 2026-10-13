package workflows

import (
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// cwProbe drives a CapacityWorkflow through timed steps and records
// every acquire answer.
type cwProbe struct {
	env *testsuite.TestWorkflowEnvironment

	mu      sync.Mutex
	results []*AcquireResult
	n       int
}

func newCW(t *testing.T) *cwProbe {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	suite.SetLogger(log.NewStructuredLogger(slog.New(slog.DiscardHandler)))
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(CapacityWorkflow, workflow.RegisterOptions{Name: CapacityWorkflowName})
	return &cwProbe{env: env}
}

func (p *cwProbe) at(d time.Duration, fn func()) { p.env.RegisterDelayedCallback(fn, d) }

func (p *cwProbe) acquire() {
	p.n++
	p.env.UpdateWorkflow(AcquireUpdate, "u"+strconv.Itoa(p.n), &testsuite.TestUpdateCallback{
		OnAccept: func() {},
		OnReject: func(error) {},
		OnComplete: func(v any, err error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			if err == nil {
				p.results = append(p.results, v.(*AcquireResult))
			}
		},
	}, &AcquireRequest{Holder: testHolder, Priority: PriorityNormal})
}

func (p *cwProbe) result(t *testing.T, i int) *AcquireResult {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if i >= len(p.results) {
		t.Fatalf("acquire %d: no answer (have %d)", i, len(p.results))
	}
	return p.results[i]
}

func (p *cwProbe) run(maxRuns int, end time.Duration) {
	p.at(end, p.env.CancelWorkflow)
	p.env.ExecuteWorkflow(CapacityWorkflowName, &CapacityWorkflowInput{MaxConcurrentRuns: maxRuns, LeaseTTL: testLeaseTTL})
}

// A full cap turns the caller away until capWait (plus jitter); a report
// frees the slot.
func TestCapacityWorkflow_DeniesAtTheCapUntilAReport(t *testing.T) {
	t.Parallel()
	p := newCW(t)
	var asked time.Time
	p.at(time.Minute, p.acquire)
	p.at(2*time.Minute, func() {
		asked = p.env.Now()
		p.acquire()
	})
	p.at(3*time.Minute, func() { p.env.SignalWorkflow(ReportSignal, &Report{LeaseID: p.result(t, 0).LeaseID}) })
	p.at(4*time.Minute, p.acquire)
	p.run(1, 10*time.Minute)

	if r := p.result(t, 0); !r.Granted || r.LeaseID == "" {
		t.Fatalf("first acquire = %+v, want a grant", r)
	}
	if r := p.result(t, 1); r.Granted || r.RetryAt.Before(asked.Add(capWait)) || r.RetryAt.After(asked.Add(capWait+time.Minute)) {
		t.Errorf("acquire at the cap = %+v, want a wait of about %v (+jitter) from %v", r, capWait, asked)
	}
	if r := p.result(t, 2); !r.Granted {
		t.Errorf("acquire after the report = %+v, want a grant", r)
	}
}

// A lease that is never reported is swept at its TTL.
func TestCapacityWorkflow_SweepsAnUnreportedLease(t *testing.T) {
	t.Parallel()
	p := newCW(t)
	p.at(time.Minute, p.acquire)
	p.at(2*time.Minute, p.acquire)
	p.at(time.Minute+testLeaseTTL+time.Second, p.acquire)
	p.run(1, 2*testLeaseTTL)

	if r := p.result(t, 1); r.Granted {
		t.Fatalf("second acquire = %+v, want a wait", r)
	}
	if r := p.result(t, 2); !r.Granted {
		t.Errorf("acquire after the TTL = %+v, want a grant: the lease was swept", r)
	}
}

// The cap Signal changes the cap of the running workflow; 0 lifts it.
func TestCapacityWorkflow_CapSignal(t *testing.T) {
	t.Parallel()
	p := newCW(t)
	p.at(time.Minute, p.acquire)
	p.at(2*time.Minute, func() { p.env.SignalWorkflow(CapacitySignal, &Capacity{MaxConcurrentRuns: 2}) })
	p.at(3*time.Minute, p.acquire)
	p.at(4*time.Minute, p.acquire)
	p.at(5*time.Minute, func() { p.env.SignalWorkflow(CapacitySignal, &Capacity{MaxConcurrentRuns: 0}) })
	p.at(6*time.Minute, p.acquire)
	p.run(1, 10*time.Minute)

	for i, want := range []bool{true, true, false, true} {
		if r := p.result(t, i); r.Granted != want {
			t.Errorf("acquire %d = %+v, want granted %v", i, r, want)
		}
	}
}
