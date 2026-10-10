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
)

// repo is RepoWorkflow's state within one execution.
type repo struct {
	s       RepoState
	cadence time.Duration

	discovered workflow.ReceiveChannel
	recheck    workflow.ReceiveChannel

	// nextCheck delays the absence check after a CheckRepo failure.
	nextCheck time.Time
	// acquires numbers acquire Updates for their UpdateIDs.
	acquires int
	phase    string
}

// RepoWorkflow owns one repository: it waits for its due time or a
// recheck, takes a lease from the installation's budget, runs Renovate
// once per lease, reports the spend, and decides the next run from the
// outcome (DESIGN-0001 § RepoWorkflow, § Convergence and stall). It ends
// only when the repository is confirmed gone or offboarded, and
// continues as new to bound its history.
func RepoWorkflow(ctx workflow.Context, in *RepoState) error {
	r := &repo{
		s:          *in,
		discovered: workflow.GetSignalChannel(ctx, DiscoveredSignal),
		recheck:    workflow.GetSignalChannel(ctx, RecheckSignal),
	}
	if r.s.Platform == "" {
		r.s.Platform = Platform
	}
	if err := workflow.SetQueryHandler(ctx, StateQuery, func() (RepoState, error) { return r.s, nil }); err != nil {
		return err
	}
	if err := r.plan(ctx, PriorityNormal); err != nil {
		return err
	}
	if r.s.NextDue.IsZero() {
		r.s.NextDue = workflow.Now(ctx).Add(firstDueJitter(r.s.RepoID, r.cadence))
	}
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
	return nil
}

// wait blocks until the repository is due or rechecked. It applies
// discovered signals and runs the absence check meanwhile; end is true
// when CheckRepo confirmed the repository gone or offboarded.
func (r *repo) wait(ctx workflow.Context) (prio Priority, end bool) {
	for {
		now := workflow.Now(ctx)
		if r.drainRecheck() {
			return PriorityHigh, false
		}
		if !r.s.NextDue.After(now) {
			return PriorityNormal, false
		}
		tctx, cancel := workflow.WithCancel(ctx)
		sel := workflow.NewSelector(ctx)
		fired := ""
		sel.AddFuture(workflow.NewTimer(tctx, r.s.NextDue.Sub(now)), func(workflow.Future) { fired = "due" })
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
		sel.Select(ctx)
		cancel()

		switch fired {
		case "due":
			return PriorityNormal, false
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
// repository. Gone or no config ends the workflow; present resets
// LastSeen; a failure retries after another discovery interval, so an
// outage never looks like an offboarding.
func (r *repo) checkRepo(ctx workflow.Context) (gone bool) {
	r.upsert(ctx, PhaseChecking)
	defer r.upsert(ctx, PhaseWaiting)
	var res CheckRepoResult
	err := workflow.ExecuteActivity(shortOptions(ctx, PriorityNormal, r.s.InstallationID), CheckRepoActivity,
		&CheckRepoInput{InstallationID: r.s.InstallationID, RepoID: r.s.RepoID}).Get(ctx, &res)
	now := workflow.Now(ctx)
	if err != nil {
		workflow.GetLogger(ctx).Warn("CheckRepo failed; keeping the repository", "error", err)
		r.nextCheck = now.Add(r.s.DiscoveryInterval)
		return false
	}
	switch res.State {
	case RepoGone, RepoNoConfig:
		workflow.GetLogger(ctx).Info("offboarded", "repo_id", r.s.RepoID, "state", res.State)
		return true
	default:
		workflow.GetMetricsHandler(ctx).Counter(MetricDiscoveryMissed).Inc(1)
		if res.Slug != "" {
			r.s.Slug, r.s.DefaultBranch = res.Slug, res.DefaultBranch
		}
		r.s.LastSeen = now
		r.nextCheck = time.Time{}
		return false
	}
}

// runOnce takes leases and runs until one attempt has an outcome the
// convergence table decides on. end is true when the outcome offboards
// the repository.
func (r *repo) runOnce(ctx workflow.Context, prio Priority) (end bool, err error) {
	if err := r.plan(ctx, prio); err != nil {
		return false, err
	}
	backoffs := 0
	for {
		lease, err := r.acquire(ctx, prio)
		if err != nil {
			return false, err
		}
		r.upsert(ctx, PhaseRunning)
		start := workflow.Now(ctx)
		r.s.Iterations++
		var res RunResult
		runErr := workflow.ExecuteActivity(runOptions(ctx, prio, r.s.InstallationID), RunRenovateActivity, &RunInput{
			RepoID: r.s.RepoID, Slug: r.s.Slug, DefaultBranch: r.s.DefaultBranch, InstallationID: r.s.InstallationID,
			Profile: r.s.Profile, LeaseID: lease, Attempt: r.s.Iterations,
		}).Get(ctx, &res)

		a := attemptOf(runErr, &res)
		r.report(ctx, lease, a)
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

// acquire takes a lease, sleeping to each retryAt the budget returns.
func (r *repo) acquire(ctx workflow.Context, prio Priority) (string, error) {
	r.upsert(ctx, PhaseAcquiring)
	holder := RepoWorkflowID(r.s.RepoID)
	for {
		r.acquires++
		var res AcquireResult
		err := workflow.ExecuteActivity(shortOptions(ctx, prio, r.s.InstallationID), AcquireBudgetActivity, &AcquireInput{
			InstallationID: r.s.InstallationID,
			UpdateID:       fmt.Sprintf("%s/%s/acquire-%d", holder, workflow.GetInfo(ctx).WorkflowExecution.RunID, r.acquires),
			Request:        AcquireRequest{Holder: holder, Priority: prio},
		}).Get(ctx, &res)
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

// report closes the lease on the installation's budget, with the run's
// readings when it has both, and a retryAt for a rate-limited run. The
// report is the budget's rate reading; it costs no /rate_limit call.
func (r *repo) report(ctx workflow.Context, lease string, a attempt) {
	rep := Report{LeaseID: lease}
	if res := a.result; res != nil && res.RateBefore != nil && res.RateAfter != nil {
		rep.Before, rep.After = res.RateBefore, res.RateAfter
	}
	if a.rateLimited {
		rep.RetryAt = a.retryAt
	}
	err := workflow.SignalExternalWorkflow(ctx, InstallationWorkflowID(r.s.InstallationID), "", ReportSignal, rep).Get(ctx, nil)
	if err != nil {
		workflow.GetLogger(ctx).Warn("report not delivered; the lease TTL will reclaim it", "lease", lease, "error", err)
	}
}

// decide applies the convergence table to an attempt with an outcome.
// rerun asks for a new lease at once; offboard ends the workflow.
func (r *repo) decide(ctx workflow.Context, a attempt, start time.Time) (rerun, offboard bool) {
	now := workflow.Now(ctx)
	r.s.LastRun = &RunSummary{Outcome: a.outcome, FinishedAt: now, Progress: a.progress}
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
// discovered refreshes the state, a recheck makes the next run due now.
func (r *repo) continueAsNew(ctx workflow.Context) error {
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
