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
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// RepoWorkflow constants (DESIGN-0001 OQ10).
const (
	// MaxIterations is how many runs one execution makes before it
	// continues as new.
	MaxIterations = 100
	// StallLimit is how many zero-progress timeouts in a row are rerun
	// before the repository is recorded stalled.
	StallLimit = 3
	// RerunLimit caps reruns in a row after timeouts.
	RerunLimit = 5
	// AbsenceFactor times the discovery interval without a discovered
	// signal starts CheckRepo.
	AbsenceFactor = 3

	backoffBase    = 5 * time.Minute
	pendingBackoff = 30 * time.Minute

	// capacityBackoffBase doubles on each consecutive capacity denial up
	// to capacityBackoffMax (IMPL-0002 OQ6): a full cluster cap clears as
	// runs finish, unlike a pod that never starts.
	capacityBackoffBase = time.Minute
	capacityBackoffMax  = 30 * time.Minute

	// capacityLeaseChange gates the capacity lease, so RepoWorkflows
	// started before it replay without the AcquireCapacity activity.
	capacityLeaseChange = "capacity-lease"
)

// repo is RepoWorkflow's state within one execution.
type repo struct {
	s       RepoState
	cadence time.Duration

	discovered workflow.ReceiveChannel
	recheck    workflow.ReceiveChannel
	event      workflow.ReceiveChannel
	changed    workflow.ReceiveChannel

	// settle and minInterval schedule events, from the last PlanRun.
	settle, minInterval time.Duration
	// lastStart is the last run attempt's start, min_interval's base.
	lastStart time.Time
	// runReason is the event reason of the run wait returned for, if any.
	runReason string
	// onboarding is set for a workflow started by a changed signal until
	// CheckRepo confirms the repository.
	onboarding bool

	// nextCheck delays the absence check after a CheckRepo failure.
	nextCheck time.Time
	// acquires numbers acquire Updates for their UpdateIDs.
	acquires int
	phase    string
}

// RepoWorkflow owns one repository: it waits for its due time, a
// recheck or a webhook event, takes a lease from the installation's
// budget, runs Renovate once per lease, reports the spend, and decides
// the next run from the outcome (DESIGN-0001 § RepoWorkflow, §
// Convergence and stall; DESIGN-0003 § RepoWorkflow: event and changed).
// It ends only when the repository is confirmed gone or offboarded, and
// continues as new to bound its history.
func RepoWorkflow(ctx workflow.Context, in *RepoState) error {
	r := &repo{
		s:          *in,
		discovered: workflow.GetSignalChannel(ctx, DiscoveredSignal),
		recheck:    workflow.GetSignalChannel(ctx, RecheckSignal),
		event:      workflow.GetSignalChannel(ctx, EventSignal),
		changed:    workflow.GetSignalChannel(ctx, ChangedSignal),
	}
	if r.s.Platform == "" {
		r.s.Platform = Platform
	}
	if r.s.LastRun != nil {
		r.lastStart = r.s.LastRun.StartedAt
	}
	if err := workflow.SetQueryHandler(ctx, StateQuery, func() (RepoState, error) { return r.s, nil }); err != nil {
		return err
	}
	if err := r.plan(ctx, PriorityNormal); err != nil {
		return err
	}
	r.firstDue(ctx)
	r.upsert(ctx, PhaseWaiting)

	for {
		if r.s.Iterations >= MaxIterations || workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
			return r.continueAsNew(ctx)
		}
		prio, end := r.wait(ctx)
		if end {
			return nil
		}
		end, err := r.runOnce(ctx, prio)
		if err != nil || end {
			return err
		}
		r.upsert(ctx, PhaseWaiting)
	}
}

// firstDue sets a new execution's NextDue. A workflow started by
// SignalWithStart(changed) runs CheckRepo first and the changed's event
// schedules its first run, so it skips the jitter; others are jittered
// over the cadence. A first execution logs its onboarding.
func (r *repo) firstDue(ctx workflow.Context) {
	fresh := workflow.GetInfo(ctx).ContinuedExecutionRunID == "" && r.s.LastRun == nil
	switch {
	case r.changed.Len() > 0:
		r.onboarding = fresh
		if r.s.NextDue.IsZero() {
			r.s.NextDue = workflow.Now(ctx).Add(r.cadence)
		}
	case r.s.NextDue.IsZero():
		r.s.NextDue = workflow.Now(ctx).Add(firstDueJitter(r.s.RepoID, r.cadence))
	}
	if fresh && !r.onboarding {
		workflow.GetLogger(ctx).Info("onboarded", "repo_id", r.s.RepoID, "via", "discovery")
	}
}

// firstDueJitter spreads new repositories over the cadence by a hash of
// the repository id, so the fleet does not run at once and restarts do
// not cluster (OQ4).
func firstDueJitter(repoID int64, cadence time.Duration) time.Duration {
	if cadence <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(strconv.FormatInt(repoID, 10)))
	return time.Duration(h.Sum64() % uint64(cadence)) //nolint:gosec // G115: below cadence, which is an int64
}

// plan asks PlanRun for the profile and cadence: config never travels in
// a payload, so resolution runs in an activity.
func (r *repo) plan(ctx workflow.Context, p Priority) error {
	var plan RunPlan
	err := workflow.ExecuteActivity(shortOptions(ctx, p, r.s.InstallationID), PlanRunActivity, &PlanRunInput{
		InstallationID: r.s.InstallationID, Extends: r.s.Extends, Managers: r.s.Managers,
	}).Get(ctx, &plan)
	if err != nil {
		return fmt.Errorf("plan run: %w", err)
	}
	r.s.Profile, r.cadence = plan.Profile, plan.Cadence
	r.settle, r.minInterval = plan.Settle, plan.MinInterval
	return nil
}

// wait blocks until the repository is due, rechecked or an event's run
// is due. It applies discovered and event signals, handles changed
// signals and runs the absence check meanwhile; end is true when
// CheckRepo confirmed the repository gone or offboarded. A pending
// changed is handled before the due check, so a workflow it started runs
// CheckRepo before any run.
func (r *repo) wait(ctx workflow.Context) (prio Priority, end bool) {
	r.runReason = ""
	for {
		if c, ok := r.takeChanged(); ok {
			if r.handleChanged(ctx, &c) {
				return 0, true
			}
			continue
		}
		r.drainEvents(ctx)
		now := workflow.Now(ctx)
		if r.drainRecheck() {
			return PriorityHigh, false
		}
		at, atPrio, reason := r.due()
		if !at.After(now) {
			r.runReason = reason
			return atPrio, false
		}
		tctx, cancel := workflow.WithCancel(ctx)
		sel := workflow.NewSelector(ctx)
		fired := ""
		sel.AddFuture(workflow.NewTimer(tctx, at.Sub(now)), func(workflow.Future) { fired = "due" })
		if at, ok := r.absentAt(); ok {
			sel.AddFuture(workflow.NewTimer(tctx, max(at.Sub(now), 0)), func(workflow.Future) { fired = "absent" })
		}
		sel.AddReceive(r.recheck, func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(ctx, nil)
			fired = "recheck"
		})
		sel.AddReceive(r.discovered, func(c workflow.ReceiveChannel, _ bool) {
			var d Discovered
			c.Receive(ctx, &d)
			r.applyDiscovered(&d, workflow.Now(ctx))
			fired = "discovered"
		})
		// event and changed are received at the top of the loop.
		sel.AddReceive(r.event, func(workflow.ReceiveChannel, bool) { fired = "event" })
		sel.AddReceive(r.changed, func(workflow.ReceiveChannel, bool) { fired = "changed" })
		sel.Select(ctx)
		cancel()

		switch fired {
		case "due":
			r.runReason = reason
			return atPrio, false
		case "recheck":
			return PriorityHigh, false
		case "absent":
			if gone := r.checkRepo(ctx); gone {
				return 0, true
			}
		}
	}
}

// drainRecheck consumes buffered rechecks; true when there was one.
func (r *repo) drainRecheck() bool {
	got := false
	for r.recheck.ReceiveAsync(nil) {
		got = true
	}
	return got
}

func (r *repo) applyDiscovered(d *Discovered, now time.Time) {
	if d.Slug != "" {
		r.s.Slug = d.Slug
	}
	if d.DefaultBranch != "" {
		r.s.DefaultBranch = d.DefaultBranch
	}
	if d.InstallationID != 0 {
		r.s.InstallationID = d.InstallationID
	}
	if d.DiscoveryInterval > 0 {
		r.s.DiscoveryInterval = d.DiscoveryInterval
	}
	r.s.Extends = d.Extends
	r.s.LastSeen = now
	r.nextCheck = time.Time{}
}

// absentAt is when the absence check is due: LastSeen + 3 × the
// discovery interval, or later after a failed check.
func (r *repo) absentAt() (time.Time, bool) {
	if r.s.DiscoveryInterval <= 0 || r.s.LastSeen.IsZero() {
		return time.Time{}, false
	}
	at := r.s.LastSeen.Add(AbsenceFactor * r.s.DiscoveryInterval)
	if r.nextCheck.After(at) {
		at = r.nextCheck
	}
	return at, true
}

// checkRepo runs CheckRepo after discovery stopped seeing the
// repository. Gone or no config ends the workflow; present refreshes the
// slug, branch and extends and resets LastSeen; a failure retries after
// another discovery interval, so an outage never looks like an
// offboarding.
func (r *repo) checkRepo(ctx workflow.Context) (gone bool) {
	res, err := r.check(ctx)
	now := workflow.Now(ctx)
	if err != nil {
		workflow.GetLogger(ctx).Warn("CheckRepo failed; keeping the repository", "error", err)
		r.nextCheck = now.Add(r.s.DiscoveryInterval)
		return false
	}
	switch res.State {
	case RepoGone, RepoNoConfig:
		workflow.GetLogger(ctx).Info("offboarded", "repo_id", r.s.RepoID, "reason", res.State, "state", res.State)
		return true
	default:
		workflow.GetMetricsHandler(ctx).Counter(MetricDiscoveryMissed).Inc(1)
		if res.Slug != "" {
			r.s.Slug, r.s.DefaultBranch = res.Slug, res.DefaultBranch
		}
		r.s.Extends = res.Extends
		r.s.LastSeen = now
		r.nextCheck = time.Time{}
		return false
	}
}

// check runs the CheckRepo activity in the checking phase.
func (r *repo) check(ctx workflow.Context) (*CheckRepoResult, error) {
	r.upsert(ctx, PhaseChecking)
	defer r.upsert(ctx, PhaseWaiting)
	var res CheckRepoResult
	err := workflow.ExecuteActivity(shortOptions(ctx, PriorityNormal, r.s.InstallationID), CheckRepoActivity,
		&CheckRepoInput{InstallationID: r.s.InstallationID, RepoID: r.s.RepoID}).Get(ctx, &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// runOnce takes leases and runs until one attempt has an outcome the
// convergence table decides on. end is true when the outcome offboards
// the repository.
func (r *repo) runOnce(ctx workflow.Context, prio Priority) (end bool, err error) {
	// The run covers any pending event; events that arrive during it stay
	// buffered and schedule one more run after it.
	r.clearEvent()
	countEventRun(ctx, r.runReason)
	if err := r.plan(ctx, prio); err != nil {
		return false, err
	}
	backoffs := 0
	for {
		l, err := r.acquire(ctx, prio)
		if err != nil {
			return false, err
		}
		r.upsert(ctx, PhaseRunning)
		start := workflow.Now(ctx)
		r.lastStart = start
		r.s.Iterations++
		var res RunResult
		runErr := workflow.ExecuteActivity(runOptions(ctx, prio, r.s.InstallationID), RunRenovateActivity, &RunInput{
			RepoID: r.s.RepoID, Slug: r.s.Slug, DefaultBranch: r.s.DefaultBranch, InstallationID: r.s.InstallationID,
			Profile: r.s.Profile, LeaseID: l.installation, Attempt: r.s.Iterations,
		}).Get(ctx, &res)

		a := attemptOf(runErr, &res)
		r.report(ctx, l, a)
		if a.result != nil {
			r.s.Managers = mergeManagers(r.s.Managers, a.result.Managers)
		}
		switch {
		case a.outcome != "":
			rerun, offboard := r.decide(ctx, a, start)
			if offboard {
				return true, nil
			}
			if !rerun {
				return false, nil
			}
		case a.rateLimited:
			r.upsert(ctx, PhaseBackoff)
			if err := workflow.Sleep(ctx, max(a.retryAt.Sub(workflow.Now(ctx)), 0)); err != nil {
				return false, err
			}
		default:
			r.upsert(ctx, PhaseBackoff)
			limit := r.cadence
			if a.pending {
				limit = pendingBackoff
			}
			if err := workflow.Sleep(ctx, backoff(backoffs, limit)); err != nil {
				return false, err
			}
			backoffs++
		}
		r.upsert(ctx, PhaseAcquiring)
	}
}

// backoff is min(5 min × 2^n, limit).
func backoff(n int, limit time.Duration) time.Duration {
	d := backoffBase
	for range n {
		d *= 2
		if limit > 0 && d >= limit {
			return limit
		}
	}
	if limit > 0 && d > limit {
		return limit
	}
	return d
}

// attempt is one RunRenovate return, sorted into the four cases.
type attempt struct {
	// outcome is set for a RunResult or a timeout (TimedOut).
	outcome  Outcome
	result   *RunResult
	progress Progress
	pending  bool
	// rateLimited is a rate-limited error; retryAt is its pause.
	rateLimited bool
	retryAt     time.Time
}

// attemptOf sorts RunRenovate's return (DESIGN-0001 § RepoWorkflow step
// 3): a result; pending; a timeout with the last heartbeat's progress;
// any other error, rate-limited ones with their retryAt.
func attemptOf(err error, res *RunResult) attempt {
	if err == nil {
		return attempt{outcome: res.Outcome, result: res, progress: res.Progress}
	}
	var timeout *temporal.TimeoutError
	if errors.As(err, &timeout) {
		a := attempt{outcome: OutcomeTimedOut}
		if timeout.HasLastHeartbeatDetails() {
			if err := timeout.LastHeartbeatDetails(&a.progress); err != nil {
				a.progress = Progress{}
			}
		}
		return a
	}
	var a attempt
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) {
		return a
	}
	switch appErr.Type() {
	case ErrTypePending:
		a.pending = true
		var partial RunResult
		if appErr.Details(&partial) == nil {
			a.result = &partial
		}
	case ErrTypeRateLimited:
		var rl RateLimited
		var partial RunResult
		if appErr.Details(&rl, &partial) == nil {
			a.result = &partial
		} else if err := appErr.Details(&rl); err != nil {
			rl = RateLimited{}
		}
		a.rateLimited, a.retryAt = true, rl.RetryAt
	default:
		var partial RunResult
		if appErr.Details(&partial) == nil {
			a.result = &partial
		}
	}
	return a
}

// leases are the grants one run holds: the installation's budget lease
// and, with a cluster cap, the CapacityWorkflow's.
type leases struct {
	installation string
	capacity     string
}

// acquire takes the installation lease, sleeping to each retryAt the
// budget returns, then a capacity lease. A run denied capacity gives the
// installation lease back before it waits, so a full cluster cap holds
// none of an installation's budget.
func (r *repo) acquire(ctx workflow.Context, prio Priority) (leases, error) {
	denials := 0
	for {
		inst, err := r.acquireFrom(ctx, prio, AcquireBudgetActivity)
		if err != nil {
			return leases{}, err
		}
		if workflow.GetVersion(ctx, capacityLeaseChange, workflow.DefaultVersion, 1) == workflow.DefaultVersion {
			return leases{installation: inst}, nil
		}
		res, err := r.tryAcquire(ctx, prio, AcquireCapacityActivity)
		if err == nil && res.Granted {
			return leases{installation: inst, capacity: res.LeaseID}, nil
		}
		if err != nil {
			workflow.GetLogger(ctx).Warn("acquire capacity failed", "error", err)
		}
		r.report(ctx, leases{installation: inst}, attempt{})
		r.upsert(ctx, PhaseWaitingCapacity)
		if err := workflow.Sleep(ctx, capacityBackoff(denials, r.s.RepoID)); err != nil {
			return leases{}, err
		}
		denials++
		r.upsert(ctx, PhaseAcquiring)
	}
}

// acquireFrom takes a lease through the named acquire activity, sleeping
// to each retryAt it returns.
func (r *repo) acquireFrom(ctx workflow.Context, prio Priority, activity string) (string, error) {
	r.upsert(ctx, PhaseAcquiring)
	for {
		res, err := r.tryAcquire(ctx, prio, activity)
		var wait time.Duration
		switch {
		case err != nil:
			workflow.GetLogger(ctx).Warn("acquire failed", "error", err)
			wait = backoffBase
		case res.Granted:
			return res.LeaseID, nil
		default:
			wait = max(res.RetryAt.Sub(workflow.Now(ctx)), time.Second)
		}
		if err := workflow.Sleep(ctx, wait); err != nil {
			return "", err
		}
	}
}

// tryAcquire sends one acquire through the named activity. Each call has
// its own UpdateID, so a retried activity returns the first answer and a
// new call asks again.
func (r *repo) tryAcquire(ctx workflow.Context, prio Priority, activity string) (AcquireResult, error) {
	holder := RepoWorkflowID(r.s.RepoID)
	r.acquires++
	var res AcquireResult
	err := workflow.ExecuteActivity(shortOptions(ctx, prio, r.s.InstallationID), activity, &AcquireInput{
		InstallationID: r.s.InstallationID,
		UpdateID:       fmt.Sprintf("%s/%s/acquire-%d", holder, workflow.GetInfo(ctx).WorkflowExecution.RunID, r.acquires),
		Request:        AcquireRequest{Holder: holder, Priority: prio},
	}).Get(ctx, &res)
	return res, err
}

// capacityBackoff is min(1 min × 2^n, 30 min) plus up to a tenth more,
// spread by the repository id so denied runs do not return together.
func capacityBackoff(n int, repoID int64) time.Duration {
	d := capacityBackoffBase
	for range n {
		d *= 2
		if d >= capacityBackoffMax {
			d = capacityBackoffMax
			break
		}
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(strconv.FormatInt(repoID, 10) + "/" + strconv.Itoa(n)))
	return d + time.Duration(h.Sum64()%uint64(d/10)) //nolint:gosec // G115: d/10 is a positive int64
}

// report closes the leases: the installation's with the run's readings
// when it has both, and a retryAt for a rate-limited run, and the
// capacity lease when there is one. The report is the budget's rate
// reading; it costs no /rate_limit call.
func (r *repo) report(ctx workflow.Context, l leases, a attempt) {
	rep := Report{LeaseID: l.installation}
	if res := a.result; res != nil && res.RateBefore != nil && res.RateAfter != nil {
		rep.Before, rep.After = res.RateBefore, res.RateAfter
	}
	if a.rateLimited {
		rep.RetryAt = a.retryAt
	}
	err := workflow.SignalExternalWorkflow(ctx, InstallationWorkflowID(r.s.InstallationID), "", ReportSignal, rep).Get(ctx, nil)
	if err != nil {
		workflow.GetLogger(ctx).Warn("report not delivered; the lease TTL will reclaim it", "lease", l.installation, "error", err)
	}
	if l.capacity == "" {
		return
	}
	err = workflow.SignalExternalWorkflow(ctx, CapacityWorkflowID, "", ReportSignal, Report{LeaseID: l.capacity}).Get(ctx, nil)
	if err != nil {
		workflow.GetLogger(ctx).Warn("capacity report not delivered; the lease TTL will reclaim it", "lease", l.capacity, "error", err)
	}
}

// decide applies the convergence table to an attempt with an outcome.
// rerun asks for a new lease at once; offboard ends the workflow.
func (r *repo) decide(ctx workflow.Context, a attempt, start time.Time) (rerun, offboard bool) {
	now := workflow.Now(ctx)
	r.s.LastRun = &RunSummary{Outcome: a.outcome, StartedAt: start, FinishedAt: now, Progress: a.progress}
	if a.result != nil {
		r.s.LastRun.RepositoryResult = a.result.RepositoryResult
	}
	cadence := func() {
		r.s.NextDue = start.Add(r.cadence)
		r.s.ConsecutiveReruns = 0
	}
	metrics := workflow.GetMetricsHandler(ctx)
	switch a.outcome {
	case OutcomeSucceeded:
		r.s.StallCount = 0
		cadence()
	case OutcomeTimedOut:
		progressed := a.progress.BranchesChanged+a.progress.PRsChanged > 0
		switch {
		case progressed && r.s.ConsecutiveReruns < RerunLimit:
			r.s.ConsecutiveReruns++
			return true, false
		case progressed:
			metrics.Counter(MetricReposIncomplete).Inc(1)
			workflow.GetLogger(ctx).Info("incomplete: rerun cap reached", "repo_id", r.s.RepoID)
			cadence()
		case r.s.StallCount < StallLimit:
			r.s.StallCount++
			r.s.ConsecutiveReruns++
			return true, false
		default:
			metrics.Counter(MetricReposStalled).Inc(1)
			workflow.GetLogger(ctx).Warn("stalled: no progress", "repo_id", r.s.RepoID, "stall_count", r.s.StallCount)
			cadence()
		}
	case OutcomeSkipped:
		if a.result != nil && slices.Contains(OffboardResults, a.result.RepositoryResult) {
			workflow.GetLogger(ctx).Info("offboarded", "repo_id", r.s.RepoID, "result", a.result.RepositoryResult)
			return false, true
		}
		cadence()
	default: // OutcomeFailed
		cadence()
	}
	return false, false
}

// mergeManagers is the sorted union: Managers only ever grows.
func mergeManagers(have, add []string) []string {
	out := slices.Clone(have)
	for _, m := range add {
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	slices.Sort(out)
	return out
}

// upsert records the phase and the search attributes.
func (r *repo) upsert(ctx workflow.Context, phase string) {
	if phase == r.phase && phase != PhaseWaiting {
		return
	}
	r.phase = phase
	updates := []temporal.SearchAttributeUpdate{
		SearchInstallationID.ValueSet(r.s.InstallationID),
		SearchProfile.ValueSet(r.s.Profile),
		SearchPhase.ValueSet(phase),
		SearchNextDue.ValueSet(r.s.NextDue),
	}
	if r.s.LastRun != nil {
		updates = append(updates, SearchLastOutcome.ValueSet(string(r.s.LastRun.Outcome)))
	}
	if err := workflow.UpsertTypedSearchAttributes(ctx, updates...); err != nil {
		workflow.GetLogger(ctx).Warn("search attribute upsert failed", "error", err)
	}
}

// continueAsNew carries the state, with any buffered signals applied: a
// changed runs its CheckRepo (and may end the workflow instead), an
// event schedules its run in the carried state, a discovered refreshes
// the state, a recheck makes the next run due now.
func (r *repo) continueAsNew(ctx workflow.Context) error {
	for {
		c, ok := r.takeChanged()
		if !ok {
			break
		}
		if r.handleChanged(ctx, &c) {
			return nil
		}
	}
	r.drainEvents(ctx)
	var d Discovered
	for r.discovered.ReceiveAsync(&d) {
		r.applyDiscovered(&d, workflow.Now(ctx))
	}
	if r.drainRecheck() {
		r.s.NextDue = workflow.Now(ctx)
	}
	next := r.s
	next.Iterations = 0
	return workflow.NewContinueAsNewError(ctx, RepoWorkflowName, &next)
}
