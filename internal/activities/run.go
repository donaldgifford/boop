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

package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/jobspec"
	"github.com/donaldgifford/boop/internal/kube"
	"github.com/donaldgifford/boop/internal/renovate"
	"github.com/donaldgifford/boop/internal/workflows"
)

// RunTimings are RunRenovate's clocks (DESIGN-0001 OQ10). Zero fields
// take the defaults.
type RunTimings struct {
	// Heartbeat is how often the activity heartbeats Progress; 15 s.
	Heartbeat time.Duration
	// SoftDeadline is how long after start the run is stopped; 50 min.
	SoftDeadline time.Duration
	// ExpiryMargin stops the run this long before the token expires;
	// 3 min.
	ExpiryMargin time.Duration
	// Cleanup bounds the steps after the run (rate reading, revoke,
	// delete), which run even when the activity is cancelled; 2 min.
	Cleanup time.Duration
}

// Default run timings.
const (
	DefaultHeartbeat    = 15 * time.Second
	DefaultSoftDeadline = 50 * time.Minute
	DefaultExpiryMargin = 3 * time.Minute
	DefaultCleanup      = 2 * time.Minute
)

func (t RunTimings) withDefaults() RunTimings {
	if t.Heartbeat <= 0 {
		t.Heartbeat = DefaultHeartbeat
	}
	if t.SoftDeadline <= 0 {
		t.SoftDeadline = DefaultSoftDeadline
	}
	if t.ExpiryMargin <= 0 {
		t.ExpiryMargin = DefaultExpiryMargin
	}
	if t.Cleanup <= 0 {
		t.Cleanup = DefaultCleanup
	}
	return t
}

// RunRenovate runs Renovate against one repository as a Kubernetes Job
// (DESIGN-0001 § RunRenovate activity, steps 1-12): mint a token scoped
// to the repository and the shared preset, read /rate_limit, create the
// suspended Job, its owned token Secret, unsuspend, wait for Running,
// follow and scan the log while heartbeating, stop at the soft deadline
// or on cancellation, read the exit, read /rate_limit again, revoke,
// delete the Job and classify.
//
// Classified failures are activity errors of the workflows ErrType*
// types; once the pod ran, the partial RunResult is their last detail.
func (a *Activities) RunRenovate(ctx context.Context, in *workflows.RunInput) (*workflows.RunResult, error) {
	if a.runner == nil {
		return nil, infraError("no Kubernetes runner configured", nil)
	}
	app, err := a.appFor(ctx, in.InstallationID)
	if err != nil {
		return nil, infraError(err.Error(), nil)
	}
	info := activity.GetInfo(ctx)
	r := &run{
		a:     a,
		in:    in,
		app:   app,
		name:  jobspec.JobName(in.RepoID, in.Attempt),
		start: a.now(),
		res:   &workflows.RunResult{ExitCode: -1},
		scan:  renovate.NewScanner(in.Slug),
	}
	r.log = a.log.With("repo", in.Slug, "repo_id", in.RepoID, "installation", in.InstallationID,
		"workflow_id", info.WorkflowExecution.ID, "run_id", info.WorkflowExecution.RunID,
		"attempt", in.Attempt, "job", r.name, "profile", in.Profile)
	r.lines = a.runLog.With("repo", in.Slug, "workflow_id", info.WorkflowExecution.ID,
		"run_id", info.WorkflowExecution.RunID, "attempt", in.Attempt, "job", r.name, "profile", in.Profile)
	r.workflowID, r.runID = info.WorkflowExecution.ID, info.WorkflowExecution.RunID

	a.metrics.RunStarted(in.Profile)
	defer a.metrics.RunEnded(in.Profile)
	res, err := r.execute(ctx)
	r.complete(ctx, err)
	return res, err
}

// run is one RunRenovate execution.
type run struct {
	a                 *Activities
	in                *workflows.RunInput
	app               *config.App
	name              string
	workflowID, runID string
	log, lines        *slog.Logger

	start     time.Time
	token     string
	expiresAt time.Time
	rates     RateReader
	res       *workflows.RunResult

	mu        sync.Mutex // guards scan and firstLine
	scan      *renovate.Scanner
	firstLine time.Time
	running   time.Time
}

// complete writes the one run_complete line and the run metrics.
func (r *run) complete(ctx context.Context, err error) {
	res := r.res
	outcome := string(res.Outcome)
	var appErr *temporal.ApplicationError
	switch {
	case err == nil:
	case errors.As(err, &appErr):
		outcome = appErr.Type()
	case ctx.Err() != nil:
		outcome = "canceled"
	default:
		outcome = workflows.ErrTypeInfrastructure
	}
	var overhead time.Duration
	r.mu.Lock()
	if !r.firstLine.IsZero() && !r.running.IsZero() {
		overhead = max(r.firstLine.Sub(r.running), 0)
	}
	r.mu.Unlock()
	r.a.metrics.RunCompleted(outcome, res.RepositoryResult, r.in.Profile, res.Duration, res.PodStart, overhead)

	attrs := []any{
		"outcome", outcome, "result", res.RepositoryResult, "exit_code", res.ExitCode,
		"duration", res.Duration, "pod_start", res.PodStart, "overhead", overhead,
		"renovate_version", res.RenovateVersion, "branches_changed", res.Progress.BranchesChanged,
		"prs_changed", res.Progress.PRsChanged, "tuples", len(res.Tuples), "managers", res.Managers,
		"problems", len(res.Problems), "report_missing", res.ReportMissing,
		"rate_before", res.RateBefore != nil, "rate_after", res.RateAfter != nil,
	}
	if err != nil {
		attrs = append(attrs, "error", err.Error())
	}
	r.log.InfoContext(ctx, "run_complete", attrs...)
}

func (r *run) execute(ctx context.Context) (*workflows.RunResult, error) {
	if err := r.mint(ctx); err != nil {
		return nil, infraError("mint token: "+err.Error(), nil)
	}
	r.res.RateBefore = r.readRate(ctx)

	created := r.a.now()
	if err := r.createJob(ctx); err != nil {
		r.finish(ctx)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, infraError("create job: "+err.Error(), r.res)
	}

	if _, err := r.a.runner.WaitRunning(ctx, r.name, r.a.cfg.Runs.PendingTimeout); err != nil {
		r.finish(ctx)
		switch {
		case errors.Is(err, kube.ErrPending):
			r.a.metrics.PendingTimeout()
			return nil, temporal.NewApplicationErrorWithOptions(
				fmt.Sprintf("pod not Running within %v", r.a.cfg.Runs.PendingTimeout), workflows.ErrTypePending,
				temporal.ApplicationErrorOptions{Details: []any{r.res}})
		case ctx.Err() != nil:
			return nil, ctx.Err()
		default:
			return nil, infraError("wait for pod: "+err.Error(), r.res)
		}
	}
	r.running = r.a.now()
	r.res.PodStart = r.running.Sub(created)

	end := r.follow(ctx)
	r.res.Duration = r.a.now().Sub(r.running)
	r.fillResult(end)
	r.finish(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return r.classify(end)
}

// mint mints the run's token, scoped to the repository and the shared
// preset's repository (DESIGN-0001 OQ2).
func (r *run) mint(ctx context.Context) error {
	ids := []int64{r.in.RepoID}
	if preset, err := r.a.presetRepoID(ctx, r.app, r.in.InstallationID); err != nil {
		r.a.metrics.TokenMint(resultFailure)
		return err
	} else if preset != 0 && preset != r.in.RepoID {
		ids = append(ids, preset)
	}
	minter, err := r.a.minterFor(r.app)
	if err != nil {
		r.a.metrics.TokenMint(resultFailure)
		return err
	}
	tok, exp, err := minter.Mint(ctx, r.in.InstallationID, ids)
	if err != nil {
		r.a.metrics.TokenMint(resultFailure)
		return err
	}
	r.a.metrics.TokenMint(resultSuccess)
	r.token, r.expiresAt = tok, exp
	if rr, err := r.a.gh.Token(r.app, tok); err == nil {
		r.rates = rr
	} else {
		r.log.WarnContext(ctx, "no rate reader for the run token", "error", err)
	}
	return nil
}

// Metric result labels.
const (
	resultSuccess = "success"
	resultFailure = "failure"
)

// readRate reads /rate_limit with the run's token; nil when it cannot,
// so the budget report carries no readings rather than wrong ones.
func (r *run) readRate(ctx context.Context) *workflows.Readings {
	if r.rates == nil {
		return nil
	}
	rd, err := r.rates.ReadRateLimit(ctx)
	if err != nil {
		r.log.WarnContext(ctx, "rate limit read failed", "error", err)
		return nil
	}
	return rd
}

// createJob builds the Job and creates it suspended, then its token
// Secret, then unsuspends it, so the pod never starts without the
// Secret and the Secret never outlives the Job. A Job left behind by a
// worker that died mid-attempt is deleted and recreated.
func (r *run) createJob(ctx context.Context) error {
	in, err := r.a.cfg.BuildInput(r.app, r.in.Profile, jobspec.Repo{
		ID: r.in.RepoID, Slug: r.in.Slug, DefaultBranch: r.in.DefaultBranch, InstallationID: r.in.InstallationID,
	})
	if err != nil {
		return err
	}
	in.Attempt = r.in.Attempt
	in.TokenSecret = r.name
	in.WorkflowID, in.RunID = r.workflowID, r.runID
	if r.in.DryRun != "" {
		in.DryRun = r.in.DryRun
	}
	spec, err := jobspec.BuildJob(in)
	if err != nil {
		return err
	}
	job, err := r.a.runner.CreateSuspended(ctx, spec)
	if apierrors.IsAlreadyExists(err) {
		r.log.WarnContext(ctx, "deleting a leftover job before recreating it")
		if err := r.a.runner.Delete(ctx, r.name); err != nil {
			return err
		}
		job, err = r.a.runner.CreateSuspended(ctx, spec)
	}
	if err != nil {
		return err
	}
	secret, err := jobspec.BuildTokenSecret(job, r.token)
	if err != nil {
		return err
	}
	if _, err := r.a.runner.CreateSecret(ctx, secret); err != nil {
		return err
	}
	return r.a.runner.Unsuspend(ctx, r.name)
}

// follow streams the log until the container exits, the soft deadline
// passes or ctx ends, heartbeating Progress meanwhile. A run stopped
// early has its Job deleted here, with foreground propagation, before
// anything else.
func (r *run) follow(ctx context.Context) *runEnd {
	t := r.a.timings
	deadline := r.start.Add(t.SoftDeadline)
	if !r.expiresAt.IsZero() {
		deadline = minTime(deadline, r.expiresAt.Add(-t.ExpiryMargin))
	}
	runCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() { r.heartbeat(ctx, stop) })
	defer func() {
		close(stop)
		wg.Wait()
	}()

	err := r.a.runner.FollowLog(runCtx, r.name, time.Time{}, r.line)
	if err != nil && runCtx.Err() == nil {
		r.log.WarnContext(ctx, "log stream ended early", "error", err)
	}
	end := &runEnd{}
	if runCtx.Err() == nil {
		exit, err := r.a.runner.ExitCode(runCtx, r.name)
		if err == nil {
			end.Exit, end.Exited = exit, true
		} else if runCtx.Err() == nil {
			r.log.WarnContext(ctx, "exit code unreadable", "error", err)
		}
	}
	if !end.Exited && runCtx.Err() != nil {
		end.SoftDeadline = ctx.Err() == nil
		r.log.InfoContext(ctx, "stopping run", "soft_deadline", end.SoftDeadline)
		r.deleteJob(ctx)
	}
	r.mu.Lock()
	end.Scan = r.scan.Result()
	r.mu.Unlock()
	return end
}

// line forwards one Renovate line to the run log with the correlation
// fields and feeds the scanner.
func (r *run) line(l kube.Line) error {
	var payload slog.Attr
	if json.Valid([]byte(l.Text)) {
		payload = slog.Any("renovate", json.RawMessage(l.Text))
	} else {
		payload = slog.String("renovate", l.Text)
	}
	r.lines.LogAttrs(context.Background(), slog.LevelInfo, "renovate", slog.Time("ts", l.Time), payload)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.firstLine.IsZero() {
		r.firstLine = l.Time
	}
	r.scan.Feed(l.Text)
	return nil
}

// heartbeat records Progress every Heartbeat until stop closes.
func (r *run) heartbeat(ctx context.Context, stop <-chan struct{}) {
	tick := time.NewTicker(r.a.timings.Heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-tick.C:
			r.mu.Lock()
			p := r.scan.Result().Progress
			r.mu.Unlock()
			activity.RecordHeartbeat(ctx, workflows.Progress{BranchesChanged: p.BranchesChanged, PRsChanged: p.PRsChanged})
		}
	}
}

// fillResult copies what the scanner and the exit say into the result.
func (r *run) fillResult(end *runEnd) {
	s := end.Scan
	res := r.res
	if end.Exited {
		res.ExitCode = end.Exit.Code
	}
	if s.Finished != nil {
		res.RepositoryResult = s.Finished.Result
	}
	res.RenovateVersion = s.RenovateVersion
	res.Managers = s.Managers
	res.Progress = workflows.Progress{BranchesChanged: s.Progress.BranchesChanged, PRsChanged: s.Progress.PRsChanged}
	res.ReportMissing = s.ReportMissing
	res.Tuples = make([]workflows.UpdateTuple, len(s.Tuples))
	for i := range s.Tuples {
		res.Tuples[i] = workflows.UpdateTuple(s.Tuples[i])
	}
	res.Problems = make([]workflows.Problem, len(s.Problems))
	for i := range s.Problems {
		res.Problems[i] = workflows.Problem(s.Problems[i])
	}
}

// finish reads RateAfter, revokes the token and deletes the Job, in that
// order, on a context that survives the activity's cancellation.
func (r *run) finish(ctx context.Context) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.a.timings.Cleanup)
	defer cancel()
	if r.res.RateAfter == nil && r.running != (time.Time{}) {
		r.res.RateAfter = r.readRate(cctx)
	}
	r.revoke(cctx)
	r.deleteJob(cctx)
}

// revoke ends the token early. A failure is logged and counted, not an
// error: the token expires on its own within the hour.
func (r *run) revoke(ctx context.Context) {
	if r.token == "" {
		return
	}
	minter, err := r.a.minterFor(r.app)
	if err == nil {
		err = minter.Revoke(ctx, r.token)
	}
	if err != nil {
		r.a.metrics.TokenRevocation(resultFailure)
		r.log.WarnContext(ctx, "token revocation failed", "error", err)
	} else {
		r.a.metrics.TokenRevocation(resultSuccess)
	}
	r.token = ""
}

// deleteJob deletes the Job with foreground propagation, bounded, even
// when ctx is cancelled; the Secret follows by owner reference.
func (r *run) deleteJob(ctx context.Context) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.a.timings.Cleanup)
	defer cancel()
	if err := r.a.runner.Delete(cctx, r.name); err != nil {
		r.log.ErrorContext(ctx, "job delete failed; ttlSecondsAfterFinished will collect it", "error", err)
	}
}

// classify turns the run's end into the result or an activity error.
func (r *run) classify(end *runEnd) (*workflows.RunResult, error) {
	c := classify(end, r.a.now())
	if c.Alert {
		r.log.Warn("run_alert", "reason", c.Reason)
	}
	if !c.Failed() {
		r.res.Outcome = c.Outcome
		return r.res, nil
	}
	details := []any{r.res}
	if c.ErrType == workflows.ErrTypeRateLimited {
		details = []any{workflows.RateLimited{RetryAt: c.RetryAt}, r.res}
	}
	return nil, temporal.NewApplicationErrorWithOptions(c.Reason, c.ErrType, temporal.ApplicationErrorOptions{Details: details})
}

// infraError is an ErrTypeInfrastructure activity error, carrying res
// when there is one.
func infraError(msg string, res *workflows.RunResult) error {
	var details []any
	if res != nil {
		details = []any{res}
	}
	return temporal.NewApplicationErrorWithOptions(msg, workflows.ErrTypeInfrastructure, temporal.ApplicationErrorOptions{Details: details})
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// presetRepoID is the shared preset repository's id, resolved once per
// app with the installation's client; 0 when the preset is not a
// repository on this platform (DESIGN-0001 OQ2).
func (a *Activities) presetRepoID(ctx context.Context, app *config.App, installationID int64) (int64, error) {
	slug := presetSlug(a.cfg.Renovate.SharedPreset)
	if slug == "" {
		return 0, nil
	}
	a.mu.Lock()
	id, ok := a.presetID[app.Name]
	a.mu.Unlock()
	if ok {
		return id, nil
	}
	api, err := a.installation(ctx, installationID)
	if err != nil {
		return 0, err
	}
	id, err = api.RepoIDBySlug(ctx, slug)
	if err != nil {
		return 0, fmt.Errorf("shared preset repository %s: %w", slug, err)
	}
	a.mu.Lock()
	a.presetID[app.Name] = id
	a.mu.Unlock()
	return id, nil
}

// presetSlug is owner/repo from "github>owner/repo:file.json",
// "github>owner/repo//path" or "local>owner/repo#tag"; "" for other
// preset kinds.
func presetSlug(preset string) string {
	rest, ok := strings.CutPrefix(preset, "github>")
	if !ok {
		if rest, ok = strings.CutPrefix(preset, "local>"); !ok {
			return ""
		}
	}
	if i := strings.IndexAny(rest, ":#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.Index(rest, "//"); i >= 0 {
		rest = rest[:i]
	}
	if strings.Count(rest, "/") != 1 {
		return ""
	}
	return rest
}
