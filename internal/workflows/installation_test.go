package workflows

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const (
	testLeaseTTL        = 20 * time.Minute
	testInstallation    = int64(7)
	testHolder          = "repo/github/42"
	testLimit           = 5000
	testReserveFraction = 0.10
)

// iwProbe drives an InstallationWorkflow through timed steps, answers
// its ReadRateLimit activity and records every acquire answer.
type iwProbe struct {
	env *testsuite.TestWorkflowEnvironment

	mu       sync.Mutex
	results  []*AcquireResult
	rejected []error
	n        int

	// reads counts ReadRateLimit calls; readings answers them, and nil
	// fails them without retry.
	reads    int
	readings func(now time.Time) *Readings
}

func newIW(t *testing.T) *iwProbe {
	t.Helper()

	var suite testsuite.WorkflowTestSuite

	suite.SetLogger(log.NewStructuredLogger(slog.New(slog.DiscardHandler)))

	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(InstallationWorkflow, workflow.RegisterOptions{Name: InstallationWorkflowName})

	p := &iwProbe{env: env, readings: fullBudget}

	env.RegisterActivityWithOptions(func(context.Context, *ReadRateLimitInput) (*Readings, error) {
		p.mu.Lock()
		defer p.mu.Unlock()

		p.reads++

		if p.readings == nil {
			return nil, temporal.NewNonRetryableApplicationError("github down", "test", nil)
		}

		return p.readings(env.Now()), nil
	}, activity.RegisterOptions{Name: ReadRateLimitActivity})

	return p
}

// fullBudget is a fresh hourly window with most of both resources left.
func fullBudget(now time.Time) *Readings {
	return readings(now, Reading{Limit: testLimit, Remaining: 4500, Reset: now.Add(time.Hour)},
		Reading{Limit: testLimit, Remaining: 4500, Reset: now.Add(time.Hour)})
}

func readings(now time.Time, core, graphql Reading) *Readings {
	return &Readings{Resources: map[string]Reading{ResourceCore: core, ResourceGraphQL: graphql}, ObservedAt: now}
}

// at runs fn d after the workflow starts.
func (p *iwProbe) at(d time.Duration, fn func()) {
	p.env.RegisterDelayedCallback(fn, d)
}

func (p *iwProbe) acquire(prio Priority) {
	p.n++
	p.env.UpdateWorkflow(AcquireUpdate, "u"+strconv.Itoa(p.n), &testsuite.TestUpdateCallback{
		OnAccept: func() {},
		OnReject: func(err error) {
			p.mu.Lock()
			defer p.mu.Unlock()

			p.rejected = append(p.rejected, err)
		},
		OnComplete: func(v any, err error) {
			p.mu.Lock()
			defer p.mu.Unlock()

			if err == nil {
				p.results = append(p.results, v.(*AcquireResult))
			}
		},
	}, &AcquireRequest{Holder: testHolder, Priority: prio})
}

func (p *iwProbe) report(r *Report) {
	p.env.SignalWorkflow(ReportSignal, r)
}

// run executes the workflow, cancelling it after end.
func (p *iwProbe) run(t *testing.T, in *InstallationWorkflowInput, end time.Duration) error {
	t.Helper()

	p.at(end, p.env.CancelWorkflow)
	p.env.ExecuteWorkflow(InstallationWorkflowName, in)

	return p.env.GetWorkflowError()
}

func iwInput() *InstallationWorkflowInput {
	return &InstallationWorkflowInput{InstallationID: testInstallation, ReserveFraction: testReserveFraction, LeaseTTL: testLeaseTTL}
}

func (p *iwProbe) result(t *testing.T, i int) *AcquireResult {
	t.Helper()

	p.mu.Lock()
	defer p.mu.Unlock()

	if i >= len(p.results) {
		t.Fatalf("acquire %d: no answer (have %d)", i, len(p.results))
	}

	return p.results[i]
}

func (p *iwProbe) state(t *testing.T) BudgetState {
	t.Helper()

	v, err := p.env.QueryWorkflow(StateQuery)
	if err != nil {
		t.Fatalf("query state: %v", err)
	}

	var s BudgetState
	if err := v.Get(&s); err != nil {
		t.Fatalf("decode state: %v", err)
	}

	return s
}

func (p *iwProbe) readCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.reads
}

func TestInstallationWorkflow_ReadsAtStartAndGrants(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	p.at(time.Minute, func() { p.acquire(PriorityNormal) })
	p.at(2*time.Minute, func() {
		now := p.env.Now()
		before := fullBudget(now.Add(-time.Minute))
		after := readings(now, Reading{Limit: testLimit, Remaining: 4200, Reset: before.Resources[ResourceCore].Reset},
			Reading{Limit: testLimit, Remaining: 4350, Reset: before.Resources[ResourceGraphQL].Reset})
		p.report(&Report{LeaseID: p.result(t, 0).LeaseID, Before: before, After: after})
	})
	p.at(3*time.Minute, func() { p.acquire(PriorityNormal) })

	_ = p.run(t, iwInput(), 30*time.Minute)

	if r := p.result(t, 0); !r.Granted || r.Optimistic || r.LeaseID == "" {
		t.Errorf("first acquire = %+v, want a known-budget grant: the workflow reads /rate_limit at start", r)
	}

	if r := p.result(t, 1); !r.Granted || r.Optimistic || r.LeaseID == p.result(t, 0).LeaseID {
		t.Errorf("second acquire = %+v, want a grant under a new lease", r)
	}

	if n := p.readCount(); n != 1 {
		t.Errorf("ReadRateLimit ran %d times in 30 minutes, want once at start", n)
	}

	s := p.state(t)
	if s.Resources[ResourceCore].Limit != testLimit || s.Resources[ResourceCore].Remaining != 4200 || len(s.Leases) != 1 {
		t.Errorf("state = %+v, want the report's readings applied and one open lease", s)
	}

	// 300 → 0.8×300 + 0.2×300 = 300 for core (spent 300); graphql 150 → 0.8×150 + 0.2×150 = 150.
	if e := s.Resources[ResourceCore].Estimate; e != DefaultEstimateCore {
		t.Errorf("core estimate = %v after one sample equal to the default, want %d", e, DefaultEstimateCore)
	}
}

func TestInstallationWorkflow_OptimisticWithoutReadings(t *testing.T) {
	t.Parallel()

	p := newIW(t)
	p.readings = nil

	p.at(time.Minute, func() { p.acquire(PriorityNormal) })
	p.at(2*time.Minute, func() { p.report(&Report{LeaseID: p.result(t, 0).LeaseID, After: fullBudget(p.env.Now())}) })
	p.at(3*time.Minute, func() { p.acquire(PriorityNormal) })

	_ = p.run(t, iwInput(), 15*time.Minute)

	if r := p.result(t, 0); !r.Granted || !r.Optimistic {
		t.Errorf("acquire before any reading = %+v, want an optimistic grant", r)
	}

	if r := p.result(t, 1); !r.Granted || r.Optimistic {
		t.Errorf("acquire after a report with readings = %+v, want a known-budget grant", r)
	}

	if n := p.readCount(); n != 1 {
		t.Errorf("ReadRateLimit ran %d times, want once: the report's readings pushed the retry out", n)
	}
}

func TestInstallationWorkflow_FailedReadRetriesLater(t *testing.T) {
	t.Parallel()

	p := newIW(t)
	p.readings = nil

	_ = p.run(t, iwInput(), 25*time.Minute)

	if n := p.readCount(); n != 3 {
		t.Errorf("ReadRateLimit ran %d times in 25 minutes, want 3: at start, then every %v after a failure", n, refreshRetry)
	}
}

func TestInstallationWorkflow_TightestResourceBinds(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	var reset time.Time

	p.readings = func(now time.Time) *Readings {
		reset = now.Add(30 * time.Minute)
		// core is plentiful; graphql sits exactly at the 10% reserve.
		return readings(now, Reading{Limit: testLimit, Remaining: 4500, Reset: reset},
			Reading{Limit: testLimit, Remaining: 500, Reset: reset})
	}

	p.at(time.Minute, func() { p.acquire(PriorityNormal) })
	p.at(40*time.Minute, func() { p.acquire(PriorityNormal) })

	_ = p.run(t, iwInput(), 50*time.Minute)

	if r := p.result(t, 0); r.Granted || r.RetryAt.Before(reset) || r.RetryAt.After(reset.Add(time.Minute)) {
		t.Errorf("acquire with graphql at the reserve = %+v, want a wait until its reset (+jitter) %v", r, reset)
	}

	if r := p.result(t, 1); !r.Granted || !r.Optimistic {
		t.Errorf("acquire after the reset = %+v, want an optimistic grant: the window is unknown again", r)
	}
}

func TestInstallationWorkflow_OpenLeasesCommitTheEstimate(t *testing.T) {
	t.Parallel()

	p := newIW(t)
	// Reserve 500; one run commits 300: 900 − 300 ≥ 500 admits one,
	// 900 − 600 < 500 refuses the second.
	p.readings = func(now time.Time) *Readings {
		return readings(now, Reading{Limit: testLimit, Remaining: 900, Reset: now.Add(24 * time.Hour)},
			Reading{Limit: testLimit, Remaining: 4500, Reset: now.Add(24 * time.Hour)})
	}

	p.at(time.Minute, func() { p.acquire(PriorityNormal) })
	p.at(2*time.Minute, func() { p.acquire(PriorityNormal) })
	p.at(time.Minute+testLeaseTTL+time.Minute, func() { p.acquire(PriorityNormal) })

	_ = p.run(t, iwInput(), 40*time.Minute)

	if !p.result(t, 0).Granted || p.result(t, 1).Granted {
		t.Fatalf("acquires = %+v %+v, want grant then wait while the lease commits 300 of 900", p.result(t, 0), p.result(t, 1))
	}

	if r := p.result(t, 2); !r.Granted {
		t.Errorf("acquire after the lease expired = %+v, want a grant: an unreported lease must not leak", r)
	}
}

func TestInstallationWorkflow_ConcurrencyCap(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	var asked time.Time

	p.at(time.Minute, func() { p.acquire(PriorityNormal) })
	p.at(2*time.Minute, func() {
		asked = p.env.Now()
		p.acquire(PriorityNormal)
	})
	p.at(3*time.Minute, func() { p.report(&Report{LeaseID: p.result(t, 0).LeaseID}) })
	p.at(4*time.Minute, func() { p.acquire(PriorityNormal) })

	in := iwInput()
	in.MaxConcurrentRuns = 1

	_ = p.run(t, in, 30*time.Minute)

	if r := p.result(t, 0); !r.Granted {
		t.Fatalf("first acquire = %+v, want a grant", r)
	}

	if r := p.result(t, 1); r.Granted || r.RetryAt.Before(asked.Add(capWait)) || r.RetryAt.After(asked.Add(capWait+time.Minute)) {
		t.Errorf("acquire at the cap = %+v, want a wait of about %v (+jitter) from %v", r, capWait, asked)
	}

	if r := p.result(t, 2); !r.Granted {
		t.Errorf("acquire after the report = %+v, want a grant: the report freed the slot", r)
	}
}

func TestInstallationWorkflow_SecondaryLimitPausesTheInstallation(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	var pause time.Time

	p.at(time.Minute, func() {
		pause = p.env.Now().Add(10 * time.Minute)
		p.report(&Report{LeaseID: "none", RetryAt: pause})
	})
	p.at(2*time.Minute, func() { p.acquire(PriorityHigh) })
	p.at(12*time.Minute, func() { p.acquire(PriorityHigh) })

	_ = p.run(t, iwInput(), 20*time.Minute)

	if r := p.result(t, 0); r.Granted || !r.RetryAt.Equal(pause) {
		t.Errorf("acquire during a secondary-limit pause = %+v, want a wait until %v", r, pause)
	}

	if r := p.result(t, 1); !r.Granted {
		t.Errorf("acquire after the pause = %+v, want a grant", r)
	}
}

func TestInstallationWorkflow_EstimateConverges(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	for i := range 40 {
		p.at(time.Duration(i+1)*time.Second, func() {
			now := p.env.Now()
			reset := now.Add(time.Hour)
			p.report(&Report{
				LeaseID: "none",
				Before: readings(now.Add(-time.Second), Reading{Limit: testLimit, Remaining: 4500, Reset: reset},
					Reading{Limit: testLimit, Remaining: 4500, Reset: reset}),
				After: readings(now, Reading{Limit: testLimit, Remaining: 4495, Reset: reset},
					Reading{Limit: testLimit, Remaining: 4497, Reset: reset}),
			})
		})
	}

	_ = p.run(t, iwInput(), time.Minute)

	s := p.state(t)
	if e := s.Resources[ResourceCore].Estimate; e < 5 || e > 6 {
		t.Errorf("core estimate = %v after 40 runs of 5 calls, want the EWMA near 5", e)
	}

	if e := s.Resources[ResourceGraphQL].Estimate; e < 3 || e > 4 {
		t.Errorf("graphql estimate = %v after 40 runs of 3 points, want the EWMA near 3", e)
	}
}

func TestInstallationWorkflow_SpendAcrossAResetIsUnknown(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	p.at(time.Second, func() {
		now := p.env.Now()
		p.report(&Report{
			LeaseID: "none",
			Before: readings(now.Add(-time.Minute), Reading{Limit: testLimit, Remaining: 10, Reset: now},
				Reading{Limit: testLimit, Remaining: 10, Reset: now}),
			After: readings(now, Reading{Limit: testLimit, Remaining: 4990, Reset: now.Add(time.Hour)},
				Reading{Limit: testLimit, Remaining: 4990, Reset: now.Add(time.Hour)}),
		})
	})

	_ = p.run(t, iwInput(), time.Minute)

	s := p.state(t)
	if e := s.Resources[ResourceCore].Estimate; e != DefaultEstimateCore {
		t.Errorf("core estimate = %v, want %d untouched: a window that rolled over mid-run is not a sample", e, DefaultEstimateCore)
	}

	if s.Resources[ResourceCore].Remaining != 4990 {
		t.Errorf("remaining = %d, want the after readings applied regardless", s.Resources[ResourceCore].Remaining)
	}
}

func TestInstallationWorkflow_RefreshesHourly(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	var at30, at85, at95 int

	p.at(30*time.Minute, func() {
		at30 = p.readCount()
		p.report(&Report{LeaseID: "none", After: fullBudget(p.env.Now())})
	})
	p.at(85*time.Minute, func() { at85 = p.readCount() })
	p.at(95*time.Minute, func() { at95 = p.readCount() })

	_ = p.run(t, iwInput(), 100*time.Minute)

	if at30 != 1 || at85 != 1 || at95 != 2 {
		t.Errorf("reads at 30/85/95 min = %d/%d/%d, want 1/1/2: a report's readings restart the hourly refresh", at30, at85, at95)
	}
}

func TestInstallationWorkflow_ContinueAsNewKeepsState(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	const maxHandled = 20

	p.at(time.Second, func() { p.acquire(PriorityNormal) })

	for i := range maxHandled - 1 {
		p.at(time.Duration(i+2)*time.Second, func() { p.report(&Report{LeaseID: "unknown"}) })
	}

	in := iwInput()
	in.MaxHandled = maxHandled
	in.MaxConcurrentRuns = 4
	in.DefaultEstimates = map[string]float64{ResourceCore: 250}

	err := p.run(t, in, time.Hour)

	var can *workflow.ContinueAsNewError
	if !errors.As(err, &can) {
		t.Fatalf("workflow error = %v, want ContinueAsNew after %d handled", err, maxHandled)
	}

	var next InstallationWorkflowInput
	if err := converter.GetDefaultDataConverter().FromPayloads(can.Input, &next); err != nil {
		t.Fatalf("decode: %v", err)
	}

	lease := p.result(t, 0).LeaseID
	if _, ok := next.State.Leases[lease]; !ok || len(next.State.Leases) != 1 {
		t.Errorf("carried leases = %+v, want %s", next.State.Leases, lease)
	}

	core := next.State.Resources[ResourceCore]
	if core == nil || core.Limit != testLimit || core.Estimate != 250 || next.State.Observed.IsZero() {
		t.Errorf("carried core budget = %+v, want the start readings and the configured estimate", core)
	}

	if next.InstallationID != testInstallation || next.LeaseTTL != testLeaseTTL || next.ReserveFraction != testReserveFraction ||
		next.MaxHandled != maxHandled || next.MaxConcurrentRuns != 4 || next.DefaultEstimates[ResourceCore] != 250 {
		t.Errorf("carried config = %+v", next)
	}
}

func TestInstallationWorkflow_ValidatorRejectsBadRequests(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	reject := func(req *AcquireRequest) {
		p.n++
		p.env.UpdateWorkflow(AcquireUpdate, "bad"+strconv.Itoa(p.n), &testsuite.TestUpdateCallback{
			OnAccept: func() {},
			OnReject: func(err error) {
				p.mu.Lock()
				defer p.mu.Unlock()

				p.rejected = append(p.rejected, err)
			},
			OnComplete: func(any, error) {},
		}, req)
	}

	p.at(time.Second, func() { reject(&AcquireRequest{Holder: testHolder, Priority: 9}) })
	p.at(2*time.Second, func() { reject(&AcquireRequest{Priority: PriorityNormal}) })

	_ = p.run(t, iwInput(), time.Minute)

	if len(p.rejected) != 2 || len(p.results) != 0 {
		t.Errorf("rejected = %v, results = %v; want both bad requests rejected before any lease", p.rejected, p.results)
	}
}

func TestInstallationWorkflow_SuspendedWaits(t *testing.T) {
	t.Parallel()

	p := newIW(t)

	p.at(time.Minute, func() { p.env.SignalWorkflow(SuspendSignal, Suspend{Suspended: true}) })
	p.at(2*time.Minute, func() { p.acquire(PriorityHigh) })
	p.at(3*time.Minute, func() { p.env.SignalWorkflow(SuspendSignal, Suspend{Suspended: false}) })
	p.at(4*time.Minute, func() { p.acquire(PriorityHigh) })

	_ = p.run(t, iwInput(), 10*time.Minute)

	if r := p.result(t, 0); r.Granted || r.RetryAt.IsZero() {
		t.Errorf("acquire while suspended = %+v, want a wait", r)
	}

	if r := p.result(t, 1); !r.Granted {
		t.Errorf("acquire after unsuspend = %+v, want a grant", r)
	}
}
