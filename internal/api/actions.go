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

package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/converter"

	"github.com/donaldgifford/boop/internal/api/gen"
	"github.com/donaldgifford/boop/internal/workflows"
)

// Temporal is what the API asks of Temporal: the state Query and the
// recheck signal here, health and pollers for /status. client.Client
// satisfies it.
type Temporal interface {
	QueryWorkflow(ctx context.Context, workflowID, runID, queryType string, args ...any) (converter.EncodedValue, error)
	SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg any) error
}

// Deadlines on Temporal calls (DESIGN-0004 § The API).
const (
	liveStateTimeout = 2 * time.Second
	recheckTimeout   = 5 * time.Second
	// recheckWindow is the per-replica dedupe: one recheck per repository
	// per window.
	recheckWindow = time.Minute
)

// Problem types a client can tell apart.
const (
	problemWorkflowMissing = "workflow_missing"
	problemTemporal        = "temporal_unavailable"
)

// GetRepository is one repository with its live state from the
// workflow's state Query, when Temporal answers within 2 s.
func (s *server) GetRepository(ctx context.Context, req gen.GetRepositoryRequestObject) (gen.GetRepositoryResponseObject, error) {
	row, err := s.visibleRepo(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	r := repository(row)
	out := gen.GetRepository200JSONResponse{
		Id: r.Id, InstallationId: r.InstallationId, Owner: r.Owner, Slug: r.Slug, State: r.State, Profile: r.Profile,
		OnboardedAt: r.OnboardedAt, OffboardedAt: r.OffboardedAt, NextDue: r.NextDue, LastRunId: r.LastRunId,
		LastRunAt: r.LastRunAt, LastOutcome: r.LastOutcome, OpenPrs: r.OpenPrs, Waiting: r.Waiting,
	}
	if v, ok := s.liveState(ctx, req.Id); ok {
		out.Live, out.Phase = true, &v.Phase
		out.RunningSince, out.BudgetWaitUntil = nonZero(v.RunningSince), nonZero(v.BudgetWaitUntil)
	}
	return out, nil
}

// liveState queries the repository's workflow; false when Temporal is
// absent, slow or has no such workflow.
func (s *server) liveState(ctx context.Context, id int64) (*workflows.RepoView, bool) {
	if s.opts.Temporal == nil {
		return nil, false
	}
	qctx, cancel := context.WithTimeout(ctx, liveStateTimeout)
	defer cancel()
	val, err := s.opts.Temporal.QueryWorkflow(qctx, workflows.RepoWorkflowID(id), "", workflows.StateQuery)
	if err != nil {
		s.opts.Logger.DebugContext(ctx, "repository state query failed", "repo_id", id, "error", err)
		return nil, false
	}
	var v workflows.RepoView
	if err := val.Get(&v); err != nil {
		s.opts.Logger.WarnContext(ctx, "repository state undecodable", "repo_id", id, "error", err)
		return nil, false
	}
	return &v, true
}

func nonZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// rechecks is the per-replica recheck dedupe: the last signalled
// request per repository.
type rechecks struct {
	mu   sync.Mutex
	last map[int64]recheckSent
}

type recheckSent struct {
	at        time.Time
	requestID string
}

// recent is the request a recheck of id within the window already sent.
func (d *rechecks) recent(id int64, now time.Time) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.last[id]
	if !ok || now.Sub(r.at) >= recheckWindow {
		return "", false
	}
	return r.requestID, true
}

func (d *rechecks) sent(id int64, now time.Time, requestID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.last == nil {
		d.last = make(map[int64]recheckSent)
	}
	for k, r := range d.last {
		if now.Sub(r.at) >= recheckWindow {
			delete(d.last, k)
		}
	}
	d.last[id] = recheckSent{at: now, requestID: requestID}
}

// RecheckRepository signals the repository's workflow for a run now,
// with the caller as actor and the request ID, at most once per
// repository per minute on this replica.
func (s *server) RecheckRepository(
	ctx context.Context, req gen.RecheckRepositoryRequestObject,
) (gen.RecheckRepositoryResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.visibleRepo(ctx, req.Id); err != nil {
		return nil, err
	}
	now := s.opts.Now()
	if id, ok := s.rechecks.recent(req.Id, now); ok {
		return gen.RecheckRepository202JSONResponse{RepoId: req.Id, RequestId: id, Deduplicated: true}, nil
	}
	if s.opts.Temporal == nil {
		return nil, typedError(http.StatusServiceUnavailable, problemTemporal, "Temporal is not configured")
	}
	requestID := requestIDFrom(ctx)
	actor := p.Name
	if actor == "" {
		actor = p.Subject
	}
	sctx, cancel := context.WithTimeout(ctx, recheckTimeout)
	defer cancel()
	err = s.opts.Temporal.SignalWorkflow(sctx, workflows.RepoWorkflowID(req.Id), "", workflows.RecheckSignal,
		workflows.Recheck{Actor: actor, RequestID: requestID})
	if notFound := new(serviceerror.NotFound); errors.As(err, &notFound) {
		return nil, typedError(http.StatusConflict, problemWorkflowMissing, "the repository has no running workflow")
	}
	if err != nil {
		s.opts.Logger.WarnContext(ctx, "recheck signal failed", "repo_id", req.Id, "error", err)
		return nil, typedError(http.StatusServiceUnavailable, problemTemporal, "Temporal is unreachable")
	}
	s.rechecks.sent(req.Id, now, requestID)
	s.opts.Logger.InfoContext(ctx, "recheck signalled", "repo_id", req.Id, "actor", actor, "request_id", requestID)
	return gen.RecheckRepository202JSONResponse{RepoId: req.Id, RequestId: requestID}, nil
}
