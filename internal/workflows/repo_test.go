package workflows

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const (
	testRepoID  = int64(42)
	testCadence = 24 * time.Hour
	testEvery   = 6 * time.Hour
)

// runReply is one scripted RunRenovate return.
type runReply struct {
	res *RunResult
	err error
}

func succeeded() runReply {
	return runReply{res: &RunResult{
		Outcome: OutcomeSucceeded, RepositoryResult: "done", Managers: []string{"npm"},
		RateBefore: readings(time.Time{}, Reading{Limit: 5000, Remaining: 4000}, Reading{Limit: 5000, Remaining: 4000}),
		RateAfter:  readings(time.Time{}, Reading{Limit: 5000, Remaining: 3900}, Reading{Limit: 5000, Remaining: 3950}),
	}}
}

func timedOut(progress int) runReply {
	return runReply{res: &RunResult{Outcome: OutcomeTimedOut, Progress: Progress{BranchesChanged: progress}}}
}

func skipped(result string) runReply {
	return runReply{res: &RunResult{Outcome: OutcomeSkipped, RepositoryResult: result}}
}

func failedRun() runReply {
	return runReply{res: &RunResult{Outcome: OutcomeFailed, RepositoryResult: "config-validation"}}
}

func appErr(typ string, details ...any) runReply {
	return runReply{err: temporal.NewNonRetryableApplicationError("scripted "+typ, typ, nil, details...)}
}

// rwProbe drives a RepoWorkflow with scripted activities.
type rwProbe struct {
	t   *testing.T
	env *testsuite.TestWorkflowEnvironment

	mu       sync.Mutex
	script   []runReply
	runs     []*RunInput
	runAt    []time.Time
	acquires []AcquireRequest
	reports  []Report
	plans    int
	checks   []CheckRepoResult
	checkErr error
	checked  []time.Time
	grantAt  []time.Time // acquire answers retryAt from this queue before granting
	managers [][]string
}

func newRW(t *testing.T, script ...runReply) *rwProbe {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	suite.SetLogger(log.NewStructuredLogger(slog.New(slog.DiscardHandler)))
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(RepoWorkflow, workflow.RegisterOptions{Name: RepoWorkflowName})
	p := &rwProbe{t: t, env: env, script: script}

	env.RegisterActivityWithOptions(func(_ context.Context, in *PlanRunInput) (*RunPlan, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.plans++
		p.managers = append(p.managers, slices.Clone(in.Managers))
		return &RunPlan{Profile: "default", Cadence: testCadence}, nil
	}, activity.RegisterOptions{Name: PlanRunActivity})

	env.RegisterActivityWithOptions(func(_ context.Context, in *AcquireInput) (*AcquireResult, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.acquires = append(p.acquires, in.Request)
		if len(p.grantAt) > 0 {
			at := p.grantAt[0]
			p.grantAt = p.grantAt[1:]
			return &AcquireResult{RetryAt: at}, nil
		}
		return &AcquireResult{Granted: true, LeaseID: "lease-" + in.UpdateID}, nil
	}, activity.RegisterOptions{Name: AcquireBudgetActivity})

	env.RegisterActivityWithOptions(func(_ context.Context, in *RunInput) (*RunResult, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.runs = append(p.runs, in)
		p.runAt = append(p.runAt, env.Now())
		if len(p.script) == 0 {
			// Out of script: offboard, which ends the workflow.
			return skipped("disabled-no-config").res, nil
		}
		r := p.script[0]
		p.script = p.script[1:]
		return r.res, r.err
	}, activity.RegisterOptions{Name: RunRenovateActivity})

	env.RegisterActivityWithOptions(func(context.Context, *CheckRepoInput) (*CheckRepoResult, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.checked = append(p.checked, env.Now())
		if p.checkErr != nil {
			return nil, temporal.NewNonRetryableApplicationError("github down", "test", p.checkErr)
		}
		if len(p.checks) == 0 {
			return &CheckRepoResult{State: RepoPresent}, nil
		}
		c := p.checks[0]
		p.checks = p.checks[1:]
		return &c, nil
	}, activity.RegisterOptions{Name: CheckRepoActivity})

	env.OnSignalExternalWorkflow(mock.Anything, InstallationWorkflowID(1), "", ReportSignal, mock.Anything).Return(
		func(_, _, _, _ string, arg any) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.reports = append(p.reports, arg.(Report))
			return nil
		})
	return p
}

func state(start time.Time) *RepoState {
	return &RepoState{
		RepoID: testRepoID, Slug: "acme/app", DefaultBranch: "main", InstallationID: 1,
		DiscoveryInterval: testEvery, LastSeen: start, NextDue: start,
	}
}

// run executes the workflow from in until it ends, or for at most d of
// workflow time.
func (p *rwProbe) run(in *RepoState, d time.Duration) error {
	p.env.SetTestTimeout(30 * time.Second)
	p.env.SetStartTime(in.LastSeen)
	p.env.RegisterDelayedCallback(func() { p.env.CancelWorkflow() }, d)
	p.env.ExecuteWorkflow(RepoWorkflowName, in)
	return p.env.GetWorkflowError()
}

// keepSeen sends discovered every discovery interval so the absence
// check never fires.
func (p *rwProbe) keepSeen(until time.Duration) {
	for at := testEvery; at < until; at += testEvery {
		p.env.RegisterDelayedCallback(func() {
			p.env.SignalWorkflow(
				DiscoveredSignal,
				Discovered{Slug: "acme/app", DefaultBranch: "main", InstallationID: 1, DiscoveryInterval: testEvery},
			)
		}, at)
	}
}

func (p *rwProbe) query() RepoState {
	p.t.Helper()
	v, err := p.env.QueryWorkflow(StateQuery)
	if err != nil {
		p.t.Fatalf("query: %v", err)
	}
	var s RepoState
	if err := v.Get(&s); err != nil {
		p.t.Fatal(err)
	}
	return s
}

func gaps(ts []time.Time) []time.Duration {
	out := make([]time.Duration, 0, len(ts))
	for i := 1; i < len(ts); i++ {
		out = append(out, ts[i].Sub(ts[i-1]).Round(time.Minute))
	}
	return out
}

var t0 = time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)

func isCanceled(err error) bool {
	var c *temporal.CanceledError
	return errors.As(err, &c)
}

// Succeeded: the next run is a cadence after this one's start, the
// report carries the readings and the managers merge.
func TestRepoWorkflow_Succeeded(t *testing.T) {
	t.Parallel()
	p := newRW(t, succeeded(), succeeded())
	p.keepSeen(3 * testCadence)
	var mid RepoState
	p.env.RegisterDelayedCallback(func() { mid = p.query() }, testCadence/2)
	if err := p.run(state(t0), 3*testCadence); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if got := gaps(p.runAt); !slices.Equal(got, []time.Duration{testCadence, testCadence}) {
		t.Errorf("gaps between runs = %v, want a cadence each", got)
	}
	if mid.StallCount != 0 || mid.ConsecutiveReruns != 0 || mid.LastRun == nil || mid.LastRun.Outcome != OutcomeSucceeded {
		t.Errorf("state after a success = %+v", mid)
	}
	if !mid.NextDue.Equal(p.runAt[0].Add(testCadence)) {
		t.Errorf("NextDue = %v, want start + cadence %v", mid.NextDue, p.runAt[0].Add(testCadence))
	}
	if !slices.Equal(mid.Managers, []string{"npm"}) {
		t.Errorf("managers = %v, want merged from the result", mid.Managers)
	}
	if len(p.reports) != 3 || p.reports[0].Before == nil || p.reports[0].After == nil || p.reports[0].LeaseID == "" {
		t.Errorf("reports = %+v, want one per run with readings", p.reports)
	}
	if p.runs[0].Profile != "default" || p.runs[0].Slug != "acme/app" {
		t.Errorf("run input = %+v", p.runs[0])
	}
}

// TimedOut with progress reruns at once up to the cap, then waits a
// cadence (incomplete).
func TestRepoWorkflow_TimedOutWithProgress(t *testing.T) {
	t.Parallel()
	script := make([]runReply, 0, 7)
	for range RerunLimit + 1 {
		script = append(script, timedOut(2))
	}
	script = append(script, succeeded())
	p := newRW(t, script...)
	p.keepSeen(3 * testCadence)
	var afterCap RepoState
	p.env.RegisterDelayedCallback(func() { afterCap = p.query() }, time.Hour)
	if err := p.run(state(t0), 3*testCadence); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	want := make([]time.Duration, RerunLimit, RerunLimit+2)
	want = append(want, testCadence, testCadence)
	if got := gaps(p.runAt); !slices.Equal(got, want) {
		t.Errorf("gaps = %v, want %d immediate reruns then a cadence", got, RerunLimit)
	}
	if afterCap.ConsecutiveReruns != 0 || afterCap.StallCount != 0 {
		t.Errorf("after the cap: reruns %d stall %d, want 0 0", afterCap.ConsecutiveReruns, afterCap.StallCount)
	}
}

// TimedOut without progress reruns StallLimit times, then records
// stalled; the stall count stays, so the next zero-progress timeout
// waits a cadence without reruns; a success resets it.
func TestRepoWorkflow_Stalled(t *testing.T) {
	t.Parallel()
	script := make([]runReply, 0, StallLimit+3)
	for range StallLimit + 1 {
		script = append(script, timedOut(0))
	}
	script = append(script, timedOut(0), succeeded())
	p := newRW(t, script...)
	p.keepSeen(4 * testCadence)
	var stalled, reset RepoState
	p.env.RegisterDelayedCallback(func() { stalled = p.query() }, time.Hour)
	p.env.RegisterDelayedCallback(func() { reset = p.query() }, 2*testCadence+time.Hour)
	if err := p.run(state(t0), 4*testCadence); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	want := make([]time.Duration, StallLimit, StallLimit+3)
	want = append(want, testCadence, testCadence, testCadence)
	if got := gaps(p.runAt); !slices.Equal(got, want) {
		t.Errorf("gaps = %v, want %d immediate reruns then one attempt per cadence", got, StallLimit)
	}
	if stalled.StallCount != StallLimit || stalled.ConsecutiveReruns != 0 {
		t.Errorf("stalled: stall %d reruns %d, want %d 0", stalled.StallCount, stalled.ConsecutiveReruns, StallLimit)
	}
	if reset.StallCount != 0 {
		t.Errorf("after a success: stall %d, want 0", reset.StallCount)
	}
}

// Skipped with an offboarding result ends the workflow; other skips and
// failures wait a cadence.
func TestRepoWorkflow_SkippedAndFailed(t *testing.T) {
	t.Parallel()
	p := newRW(t, skipped("fork"), failedRun(), skipped("archived"))
	p.keepSeen(4 * testCadence)
	if err := p.run(state(t0), 10*testCadence); err != nil {
		t.Fatalf("workflow: %v, want it to end on the offboarding skip", err)
	}
	if got := gaps(p.runAt); !slices.Equal(got, []time.Duration{testCadence, testCadence}) {
		t.Errorf("gaps = %v, want a cadence after the skip and after the failure", got)
	}
	if !p.env.IsWorkflowCompleted() || len(p.runs) != 3 {
		t.Errorf("completed %v after %d runs, want completion after 3", p.env.IsWorkflowCompleted(), len(p.runs))
	}
}

// pending releases the lease, backs off 5 min then 10 min, and
// re-acquires; reruns and stalls are untouched.
func TestRepoWorkflow_PendingBackoff(t *testing.T) {
	t.Parallel()
	p := newRW(t, appErr(ErrTypePending), appErr(ErrTypePending), succeeded())
	p.keepSeen(2 * testCadence)
	if err := p.run(state(t0), 2*testCadence); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if got := gaps(p.runAt); len(got) < 2 || got[0] != 5*time.Minute || got[1] != 10*time.Minute {
		t.Errorf("gaps = %v, want 5m then 10m backoff", got)
	}
	if len(p.acquires) < 3 {
		t.Errorf("acquires = %d, want one per attempt", len(p.acquires))
	}
	if r := p.reports[0]; r.Before != nil || r.After != nil || r.LeaseID == "" {
		t.Errorf("pending report = %+v, want the lease closed without readings", r)
	}
}

// An infrastructure error backs off and re-acquires; a rate-limited one
// sleeps to its retryAt and reports it to the budget.
func TestRepoWorkflow_InfrastructureAndRateLimited(t *testing.T) {
	t.Parallel()
	retryAt := t0.Add(2 * time.Hour)
	p := newRW(t,
		appErr(ErrTypeInfrastructure, &RunResult{RateBefore: readings(t0, Reading{}, Reading{})}),
		appErr(ErrTypeRateLimited, RateLimited{RetryAt: retryAt}, &RunResult{}),
		succeeded())
	p.keepSeen(2 * testCadence)
	if err := p.run(state(t0), 2*testCadence); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if len(p.runAt) < 3 {
		t.Fatalf("runs = %d, want 3", len(p.runAt))
	}
	if g := p.runAt[1].Sub(p.runAt[0]); g != 5*time.Minute {
		t.Errorf("infrastructure backoff = %v, want 5m", g)
	}
	if !p.runAt[2].Equal(retryAt) {
		t.Errorf("rerun after the rate limit at %v, want retryAt %v", p.runAt[2], retryAt)
	}
	if !p.reports[1].RetryAt.Equal(retryAt) {
		t.Errorf("rate-limited report = %+v, want RetryAt %v", p.reports[1], retryAt)
	}
	if p.reports[0].Before != nil {
		t.Errorf("a report with only a before reading must carry none: %+v", p.reports[0])
	}
}

// A heartbeat timeout counts as TimedOut with the last heartbeat's
// progress, so it reruns.
func TestRepoWorkflow_HeartbeatTimeoutProgress(t *testing.T) {
	t.Parallel()
	p := newRW(t,
		runReply{err: temporal.NewTimeoutError(0, nil, Progress{BranchesChanged: 1})},
		succeeded())
	p.keepSeen(2 * testCadence)
	var after RepoState
	p.env.RegisterDelayedCallback(func() { after = p.query() }, time.Hour)
	if err := p.run(state(t0), 2*testCadence); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if len(p.runAt) < 2 || p.runAt[1] != p.runAt[0] {
		t.Errorf("runs at %v, want an immediate rerun after the timeout with progress", p.runAt)
	}
	if after.LastRun == nil || after.StallCount != 0 {
		t.Errorf("state = %+v, want the rerun counted as progress, not a stall", after)
	}
}

// The budget's retryAt is slept to before re-acquiring.
func TestRepoWorkflow_AcquireRetryAt(t *testing.T) {
	t.Parallel()
	p := newRW(t, succeeded())
	retry := t0.Add(90 * time.Minute)
	p.grantAt = []time.Time{retry}
	p.keepSeen(2 * testCadence)
	if err := p.run(state(t0), 2*testCadence); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if len(p.runAt) == 0 || !p.runAt[0].Equal(retry) {
		t.Errorf("first run at %v, want the budget's retryAt %v", p.runAt, retry)
	}
}

// Absence: three missed discovery intervals run CheckRepo; present keeps
// the workflow, a failure retries an interval later, gone ends it.
func TestRepoWorkflow_Absence(t *testing.T) {
	t.Parallel()
	p := newRW(t, succeeded())
	p.checks = []CheckRepoResult{{State: RepoPresent, Slug: "acme/renamed", DefaultBranch: "main"}, {State: RepoGone}}
	in := state(t0)
	in.NextDue = t0.Add(30 * 24 * time.Hour) // never due within the test
	var renamed RepoState
	p.env.RegisterDelayedCallback(func() { renamed = p.query() }, 3*testEvery+time.Hour)
	if err := p.run(in, 30*24*time.Hour); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if got := gaps(append([]time.Time{t0}, p.checked...)); !slices.Equal(got, []time.Duration{3 * testEvery, 3 * testEvery}) {
		t.Errorf("checks after %v, want after 3 intervals each", got)
	}
	if renamed.Slug != "acme/renamed" {
		t.Errorf("slug after a present check = %q, want the refreshed one", renamed.Slug)
	}
	if !p.env.IsWorkflowCompleted() {
		t.Error("workflow still running after CheckRepo said gone")
	}
}

func TestRepoWorkflow_AbsenceCheckFailureKeepsWaiting(t *testing.T) {
	t.Parallel()
	p := newRW(t)
	p.checkErr = errors.New("github down")
	in := state(t0)
	in.NextDue = t0.Add(30 * 24 * time.Hour)
	err := p.run(in, 3*testEvery+3*testEvery+time.Minute)
	if !isCanceled(err) {
		t.Fatalf("workflow: %v, want it still running until canceled", err)
	}
	if got := gaps(append([]time.Time{t0}, p.checked...)); len(got) < 2 || got[0] != 3*testEvery || got[1] != testEvery {
		t.Errorf("checks after %v, want 3 intervals then one interval", got)
	}
}

// recheck runs at once at PriorityHigh; discovered refreshes the slug
// and extends.
func TestRepoWorkflow_RecheckAndDiscovered(t *testing.T) {
	t.Parallel()
	p := newRW(t, succeeded(), succeeded())
	in := state(t0)
	in.NextDue = t0.Add(testCadence)
	p.env.RegisterDelayedCallback(func() {
		p.env.SignalWorkflow(DiscoveredSignal, Discovered{
			Slug: "acme/moved", DefaultBranch: "trunk", InstallationID: 1, DiscoveryInterval: testEvery, Extends: []string{"x"},
		})
	}, time.Hour)
	p.env.RegisterDelayedCallback(func() { p.env.SignalWorkflow(RecheckSignal, nil) }, 2*time.Hour)
	p.keepSeen(3 * testCadence)
	if err := p.run(in, 3*testCadence); err != nil && !isCanceled(err) {
		t.Fatalf("workflow: %v", err)
	}
	if len(p.runAt) == 0 || !p.runAt[0].Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("first run at %v, want at the recheck", p.runAt)
	}
	if p.acquires[0].Priority != PriorityHigh {
		t.Errorf("recheck acquired at priority %d, want PriorityHigh", p.acquires[0].Priority)
	}
	if r := p.runs[0]; r.Slug != "acme/moved" || r.DefaultBranch != "trunk" {
		t.Errorf("run input = %+v, want the discovered slug and branch", r)
	}
	if !slices.Equal(p.managers[len(p.managers)-1], []string{"npm"}) {
		t.Errorf("PlanRun managers = %v, want the merged managers on later plans", p.managers)
	}
}

// A new repository's first run is jittered within the cadence by its id.
func TestRepoWorkflow_FirstDueJitter(t *testing.T) {
	t.Parallel()
	p := newRW(t, succeeded())
	in := state(t0)
	in.NextDue = time.Time{}
	p.keepSeen(2 * testCadence)
	if err := p.run(in, 2*testCadence); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	want := t0.Add(firstDueJitter(testRepoID, testCadence))
	if len(p.runAt) == 0 || !p.runAt[0].Equal(want) {
		t.Errorf("first run at %v, want %v", p.runAt, want)
	}
	if j := firstDueJitter(testRepoID, testCadence); j <= 0 || j >= testCadence || j != firstDueJitter(testRepoID, testCadence) {
		t.Errorf("jitter %v, want a stable value inside the cadence", j)
	}
}

// After MaxIterations runs the workflow continues as new with its state.
func TestRepoWorkflow_ContinueAsNew(t *testing.T) {
	t.Parallel()
	p := newRW(t, succeeded())
	in := state(t0)
	in.Iterations = MaxIterations - 1
	in.StallCount = 2
	in.Managers = []string{"pip_requirements"}
	p.keepSeen(2 * testCadence)
	err := p.run(in, 2*testCadence)
	var can *workflow.ContinueAsNewError
	if !errors.As(err, &can) {
		t.Fatalf("workflow: %v, want ContinueAsNew", err)
	}
	var next RepoState
	if err := converter.GetDefaultDataConverter().FromPayloads(can.Input, &next); err != nil {
		t.Fatal(err)
	}
	if next.Iterations != 0 || next.StallCount != 0 || next.RepoID != testRepoID ||
		!slices.Equal(next.Managers, []string{"npm", "pip_requirements"}) || next.LastRun == nil {
		t.Errorf("carried state = %+v", next)
	}
	if !next.NextDue.Equal(p.runAt[0].Add(testCadence)) {
		t.Errorf("carried NextDue = %v, want start + cadence", next.NextDue)
	}
}

func TestBackoff(t *testing.T) {
	t.Parallel()
	for n, want := range []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 30 * time.Minute, 30 * time.Minute} {
		if got := backoff(n, 30*time.Minute); got != want {
			t.Errorf("backoff(%d) = %v, want %v", n, got, want)
		}
	}
}

func TestMergeManagers(t *testing.T) {
	t.Parallel()
	got := mergeManagers([]string{"pip_requirements", "npm"}, []string{"npm", "gomod"})
	if !slices.Equal(got, []string{"gomod", "npm", "pip_requirements"}) {
		t.Errorf("merge = %v", got)
	}
}
