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

package workflows

import (
	"errors"
	"math"
	"strconv"
	"time"

	"go.temporal.io/sdk/workflow"
)

// Budget defaults (DESIGN-0001 § InstallationWorkflow, OQ5, OQ6, OQ10).
const (
	// DefaultEstimateCore and DefaultEstimateGraphQL seed each resource's
	// spend-per-run EWMA until it has samples (OQ6). Config overrides
	// them through InstallationWorkflowInput.DefaultEstimates.
	DefaultEstimateCore    = 300
	DefaultEstimateGraphQL = 150

	// DefaultReserveFraction is the share of each resource's limit held
	// back from runs, so discovery and the API always have budget.
	DefaultReserveFraction = 0.10

	// DefaultMaxConcurrentRuns is the per-installation cap that guards
	// GitHub's secondary limits, which /rate_limit does not show (OQ5).
	// 0 disables the cap: budget only.
	DefaultMaxConcurrentRuns = 10

	// DefaultLeaseTTL releases a grant that was never reported:
	// RunRenovate's ScheduleToStart (5 min) plus StartToClose (58 min)
	// plus a margin (OQ10), so a crashed holder cannot leak budget for
	// longer than it could have run.
	DefaultLeaseTTL = 70 * time.Minute

	// DefaultMaxHandled is the ContinueAsNew bound: the SDK's suggestion
	// or this many handled Updates and Signals, whichever comes first.
	DefaultMaxHandled = 2000

	// estimateWeight is the EWMA weight of the newest sample.
	estimateWeight = 0.2

	// refreshAfter is how long the budget goes without readings before
	// it reads /rate_limit itself; refreshRetry is the wait after a
	// failed read.
	refreshAfter = time.Hour
	refreshRetry = 10 * time.Minute

	// idleSweep is how often the lease sweep runs with nothing due.
	idleSweep = time.Hour

	// capWait bounds the wait a caller is told when only the concurrency
	// cap was hit. The design's bound is the next lease expiry, but a
	// run usually reports well before its 70-minute lease expires, so
	// the caller asks again sooner.
	capWait = 5 * time.Minute

	// waitJitterSpan spreads callers told to wait for the same instant
	// over this many seconds.
	waitJitterSpan = 60
)

// Lease is budget reserved for one granted run. It reserves no fixed
// amount: admission counts open leases against each resource's current
// estimate, so a better estimate applies to the runs already in flight.
type Lease struct {
	Holder  string
	Expires time.Time
}

// ResourceBudget is one /rate_limit resource as the budget last saw it,
// plus the EWMA of what one run spends from it.
type ResourceBudget struct {
	Limit     int // discovered; changes as the installation grows
	Remaining int
	Reset     time.Time
	Estimate  float64
}

// BudgetState is an installation's rate budget. It is
// InstallationWorkflow's ContinueAsNew state, so it carries the open
// leases.
type BudgetState struct {
	// Resources holds the tracked resources, keyed by ResourceCore and
	// ResourceGraphQL.
	Resources map[string]*ResourceBudget

	// Observed is when the newest readings were taken.
	Observed time.Time

	Leases    map[string]Lease
	NextLease int64

	// RetryAt is an installation-wide pause from a secondary limit: no
	// run is admitted before it (OQ5).
	RetryAt time.Time

	// Suspended is set while the installation is suspended; every
	// acquire waits until it is lifted.
	Suspended bool
}

// Suspend is the suspend Signal's payload.
type Suspend struct {
	Suspended bool
}

// InstallationWorkflowInput is InstallationWorkflow's input and its
// ContinueAsNew state. The budget.* config values travel here, so a
// config change takes effect when the activity next starts the
// workflow.
type InstallationWorkflowInput struct {
	InstallationID int64

	// ReserveFraction is the share of each limit held back
	// (budget.reserveFraction).
	ReserveFraction float64

	// MaxConcurrentRuns caps open leases (budget.maxConcurrentRuns);
	// 0 is budget only.
	MaxConcurrentRuns int

	// DefaultEstimates seeds each resource's EWMA
	// (budget.defaultEstimate); missing resources use the package
	// defaults.
	DefaultEstimates map[string]float64

	// LeaseTTL releases a grant that was never reported; 0 means
	// DefaultLeaseTTL.
	LeaseTTL time.Duration

	// MaxHandled overrides DefaultMaxHandled when positive.
	MaxHandled int

	State BudgetState
}

// AcquireRequest is the acquire Update's argument.
type AcquireRequest struct {
	// Holder names the caller, for the lease id: the RepoWorkflow ID.
	Holder   string
	Priority Priority
}

// AcquireResult is the acquire Update's result: a lease, or a time to
// try again.
type AcquireResult struct {
	Granted bool
	LeaseID string
	RetryAt time.Time

	// Optimistic marks a grant made while a resource's window was
	// unknown: no readings yet, or the last reset has passed.
	Optimistic bool
}

// Report is the report Signal's payload: a run ended. It closes the
// lease; with readings from before and after the run it also refreshes
// the budget and adds a spend sample. RetryAt, when set, pauses the
// whole installation: the run hit a secondary limit (OQ5).
type Report struct {
	LeaseID string
	Before  *Readings
	After   *Readings
	RetryAt time.Time
}

// budget is InstallationWorkflow's state within one run.
type budget struct {
	in      InstallationWorkflowInput
	handled int

	// refresh is the in-flight ReadRateLimit, nil between reads;
	// nextRefresh is when the next one is due.
	refresh       workflow.Future
	cancelRefresh workflow.CancelFunc
	nextRefresh   time.Time
}

// InstallationWorkflow holds one installation's rate budget and serves
// acquire and report (DESIGN-0001 § InstallationWorkflow). It reads
// /rate_limit itself when it starts and whenever an hour passes without
// readings, runs until the installation is removed, and continues as
// new to bound its history.
func InstallationWorkflow(ctx workflow.Context, in *InstallationWorkflowInput) error {
	b := &budget{in: *in}
	b.init()

	if err := workflow.SetQueryHandler(ctx, StateQuery, func() (BudgetState, error) { return b.in.State, nil }); err != nil {
		return err
	}

	if err := workflow.SetUpdateHandlerWithOptions(ctx, AcquireUpdate,
		func(ctx workflow.Context, req *AcquireRequest) (*AcquireResult, error) {
			b.handled++

			return b.acquire(workflow.Now(ctx), req), nil
		},
		workflow.UpdateHandlerOptions{Validator: validateAcquire},
	); err != nil {
		return err
	}

	reports := workflow.GetSignalChannel(ctx, ReportSignal)
	suspends := workflow.GetSignalChannel(ctx, SuspendSignal)

	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var s Suspend
			if !suspends.Receive(ctx, &s) {
				return
			}

			b.handled++
			b.in.State.Suspended = s.Suspended
		}
	})

	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var r Report
			if !reports.Receive(ctx, &r) {
				return
			}

			b.handled++
			b.report(&r)
		}
	})

	maxHandled := b.in.MaxHandled
	if maxHandled <= 0 {
		maxHandled = DefaultMaxHandled
	}

	done := func() bool {
		return b.handled >= maxHandled || workflow.GetInfo(ctx).GetContinueAsNewSuggested()
	}

	for {
		now := workflow.Now(ctx)
		b.startRefresh(ctx, now)

		if _, err := workflow.AwaitWithTimeout(ctx, b.untilWake(now), func() bool {
			return done() || (b.refresh != nil && b.refresh.IsReady())
		}); err != nil {
			return err
		}

		now = workflow.Now(ctx)
		b.finishRefresh(ctx, now)
		b.sweep(now)

		if done() {
			return b.continueAsNew(ctx, reports, suspends)
		}
	}
}

// init fills the defaults a fresh or carried-over state needs.
func (b *budget) init() {
	s := &b.in.State

	if s.Resources == nil {
		s.Resources = map[string]*ResourceBudget{}
	}

	for _, name := range TrackedResources {
		r := s.Resources[name]
		if r == nil {
			r = &ResourceBudget{}
			s.Resources[name] = r
		}

		if r.Estimate <= 0 {
			r.Estimate = b.defaultEstimate(name)
		}
	}

	if s.Leases == nil {
		s.Leases = map[string]Lease{}
	}

	if b.in.LeaseTTL <= 0 {
		b.in.LeaseTTL = DefaultLeaseTTL
	}

	b.nextRefresh = s.Observed.Add(refreshAfter)
}

// defaultEstimate is the configured seed for name's EWMA, or the
// package default.
func (b *budget) defaultEstimate(name string) float64 {
	if v, ok := b.in.DefaultEstimates[name]; ok && v > 0 {
		return v
	}

	if name == ResourceGraphQL {
		return DefaultEstimateGraphQL
	}

	return DefaultEstimateCore
}

// startRefresh schedules ReadRateLimit when the readings are stale and
// no read is in flight.
func (b *budget) startRefresh(ctx workflow.Context, now time.Time) {
	if b.refresh != nil || now.Before(b.nextRefresh) {
		return
	}

	rctx, cancel := workflow.WithCancel(rateLimitOptions(ctx, b.in.InstallationID))
	b.refresh = workflow.ExecuteActivity(rctx, ReadRateLimitActivity, &ReadRateLimitInput{InstallationID: b.in.InstallationID})
	b.cancelRefresh = cancel
}

// finishRefresh applies a completed ReadRateLimit. A failed read keeps
// the last readings and tries again after refreshRetry.
func (b *budget) finishRefresh(ctx workflow.Context, now time.Time) {
	if b.refresh == nil || !b.refresh.IsReady() {
		return
	}

	var r Readings

	err := b.refresh.Get(ctx, &r)
	b.refresh, b.cancelRefresh = nil, nil

	if err != nil {
		workflow.GetLogger(ctx).Warn("budget: reading /rate_limit failed; keeping the last readings",
			"installation_id", b.in.InstallationID, "error", err)

		b.nextRefresh = now.Add(refreshRetry)

		return
	}

	if !b.observe(&r) {
		b.nextRefresh = now.Add(refreshAfter)
	}
}

// continueAsNew drains pending reports and in-flight Updates first, so
// no grant or report is lost across runs. An in-flight ReadRateLimit is
// cancelled; the next run reads again if the readings are stale.
//
// NOTE: under load the server still rejects some ContinueAsNew
// completions with UNHANDLED_COMMAND, because a Signal or Update landed
// while the task was running. That is Temporal's normal handoff: the
// task retries with the new events, and an Update caught in the failed
// task errors back to its caller, which is the AcquireBudget activity
// and retries.
func (b *budget) continueAsNew(ctx workflow.Context, reports, suspends workflow.ReceiveChannel) error {
	if b.cancelRefresh != nil {
		b.cancelRefresh()
	}

	if err := workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) }); err != nil {
		return err
	}

	for {
		var r Report
		if !reports.ReceiveAsync(&r) {
			break
		}

		b.report(&r)
	}

	for {
		var s Suspend
		if !suspends.ReceiveAsync(&s) {
			break
		}

		b.in.State.Suspended = s.Suspended
	}

	return workflow.NewContinueAsNewError(ctx, InstallationWorkflowName, &b.in)
}

func validateAcquire(_ workflow.Context, req *AcquireRequest) error {
	if req == nil || req.Holder == "" {
		return errors.New("acquire: holder is required")
	}

	if req.Priority < 1 || req.Priority > 5 {
		return errors.New("acquire: priority must be 1 to 5")
	}

	return nil
}

// acquire admits a run only if every tracked resource can cover the
// open leases and this one above the reserve, the concurrency cap has
// room and no secondary-limit pause is in force (DESIGN-0001 §
// InstallationWorkflow, Admission). Otherwise it returns when to ask
// again.
func (b *budget) acquire(now time.Time, req *AcquireRequest) *AcquireResult {
	b.sweep(now)

	s := &b.in.State

	// A suspended installation's API calls fail; hold every run until
	// the suspension lifts, rechecking hourly.
	if s.Suspended {
		return &AcquireResult{RetryAt: now.Add(idleSweep)}
	}

	if now.Before(s.RetryAt) {
		return &AcquireResult{RetryAt: s.RetryAt}
	}

	if c := b.in.MaxConcurrentRuns; c > 0 && len(s.Leases) >= c {
		return &AcquireResult{RetryAt: b.jitter(earliest(b.nextExpiry(now), now.Add(capWait)))}
	}

	var (
		unknown        bool
		exhaustedUntil time.Time
	)

	for _, name := range TrackedResources {
		r := s.Resources[name]
		if r.Limit <= 0 || !now.Before(r.Reset) {
			unknown = true

			continue
		}

		reserve := math.Ceil(float64(r.Limit) * b.in.ReserveFraction)
		committed := float64(len(s.Leases)+1) * r.Estimate

		if float64(r.Remaining)-committed < reserve && r.Reset.After(exhaustedUntil) {
			exhaustedUntil = r.Reset
		}
	}

	if !exhaustedUntil.IsZero() {
		return &AcquireResult{RetryAt: b.jitter(exhaustedUntil)}
	}

	return b.grant(now, req.Holder, unknown)
}

// jitter spreads the callers told to wait for t over the minute after
// it, deterministically.
func (b *budget) jitter(t time.Time) time.Time {
	s := &b.in.State
	d := time.Duration(s.NextLease%waitJitterSpan) * time.Second
	s.NextLease++

	return t.Add(d)
}

func (b *budget) grant(now time.Time, holder string, optimistic bool) *AcquireResult {
	s := &b.in.State
	id := holder + "#" + strconv.FormatInt(s.NextLease, 10)
	s.NextLease++

	s.Leases[id] = Lease{Holder: holder, Expires: now.Add(b.in.LeaseTTL)}

	return &AcquireResult{Granted: true, LeaseID: id, Optimistic: optimistic}
}

// report closes the lease, applies a secondary-limit pause and, when
// the run carried readings, refreshes the budget and samples the spend.
func (b *budget) report(r *Report) {
	s := &b.in.State

	delete(s.Leases, r.LeaseID)

	if r.RetryAt.After(s.RetryAt) {
		s.RetryAt = r.RetryAt
	}

	if r.After == nil {
		return
	}

	if r.Before != nil {
		b.sample(r.Before, r.After)
	}

	b.observe(r.After)
}

// sample adds one run's spend per resource to its EWMA:
// before.Remaining − after.Remaining when the window did not roll over
// in between, unknown (and skipped) otherwise.
func (b *budget) sample(before, after *Readings) {
	for _, name := range TrackedResources {
		bf, okB := before.Resources[name]
		af, okA := after.Resources[name]

		if !okB || !okA || !bf.Reset.Equal(af.Reset) {
			continue
		}

		spent := bf.Remaining - af.Remaining
		if spent < 0 {
			continue
		}

		r := b.in.State.Resources[name]
		r.Estimate = estimateWeight*float64(spent) + (1-estimateWeight)*r.Estimate
	}
}

// observe applies readings newer than the budget's and reports whether
// it did; reports arrive out of order, and only the newest counts.
func (b *budget) observe(r *Readings) bool {
	s := &b.in.State

	if r.ObservedAt.IsZero() || r.ObservedAt.Before(s.Observed) {
		return false
	}

	s.Observed = r.ObservedAt
	b.nextRefresh = r.ObservedAt.Add(refreshAfter)

	for _, name := range TrackedResources {
		rd, ok := r.Resources[name]
		if !ok {
			continue
		}

		res := s.Resources[name]
		res.Remaining = rd.Remaining
		res.Reset = rd.Reset

		if rd.Limit > 0 {
			res.Limit = rd.Limit
		}
	}

	return true
}

// sweep releases leases past their expiry.
func (b *budget) sweep(now time.Time) {
	for id, l := range b.in.State.Leases {
		if !now.Before(l.Expires) {
			delete(b.in.State.Leases, id)
		}
	}
}

// nextExpiry is the earliest lease expiry, or now plus idleSweep with
// no lease open.
func (b *budget) nextExpiry(now time.Time) time.Time {
	var next time.Time

	for _, l := range b.in.State.Leases {
		if next.IsZero() || l.Expires.Before(next) {
			next = l.Expires
		}
	}

	if next.IsZero() {
		return now.Add(idleSweep)
	}

	return next
}

// untilWake is the time to the next lease expiry or the next refresh,
// whichever is first, and at least a second.
func (b *budget) untilWake(now time.Time) time.Duration {
	wake := b.nextExpiry(now)
	if b.refresh == nil && b.nextRefresh.Before(wake) {
		wake = b.nextRefresh
	}

	return max(wake.Sub(now), time.Second)
}

func earliest(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}

	return a
}
