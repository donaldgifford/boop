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

package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/workflows"
)

// MaxBody is the largest delivery ingest reads, GitHub's payload cap.
const MaxBody = 25 << 20

// DefaultDeadline bounds a delivery's Temporal calls, inside GitHub's
// 10 s.
const DefaultDeadline = 5 * time.Second

// Temporal is the part of client.Client ingest uses.
type Temporal interface {
	SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg any) error
	SignalWithStartWorkflow(ctx context.Context, workflowID, signalName string, signalArg any,
		options client.StartWorkflowOptions, workflow any, workflowArgs ...any) (client.WorkflowRun, error)
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow any, args ...any) (client.WorkflowRun, error)
}

// Metrics is what ingest records; observability.Metrics implements it.
type Metrics interface {
	Delivery(app, event, outcome string)
	TemporalCall(action string, d time.Duration)
}

// NopMetrics records nothing.
type NopMetrics struct{}

// Delivery implements Metrics.
func (NopMetrics) Delivery(string, string, string) {}

// TemporalCall implements Metrics.
func (NopMetrics) TemporalCall(string, time.Duration) {}

// Options configure a Handler. Config must have had ReadWebhookSecrets
// called.
type Options struct {
	Config    *config.Config
	Temporal  Temporal
	TaskQueue string
	// Sink receives every delivery's record; a LogSink on Logger when nil.
	Sink    Sink
	Metrics Metrics
	Logger  *slog.Logger
	// Deadline bounds the Temporal calls; DefaultDeadline when zero.
	Deadline time.Duration
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// Handler is the webhook endpoint (DESIGN-0003 § The ingest role).
type Handler struct {
	cfg       *config.Config
	temporal  Temporal
	taskQueue string
	sink      Sink
	metrics   Metrics
	log       *slog.Logger
	deadline  time.Duration
	now       func() time.Time
}

// NewHandler builds the handler from opts.
func NewHandler(opts *Options) *Handler {
	h := &Handler{
		cfg:       opts.Config,
		temporal:  opts.Temporal,
		taskQueue: opts.TaskQueue,
		sink:      opts.Sink,
		metrics:   opts.Metrics,
		log:       opts.Logger,
		deadline:  opts.Deadline,
		now:       opts.Now,
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	if h.sink == nil {
		h.sink = LogSink{Logger: h.log}
	}
	if h.metrics == nil {
		h.metrics = NopMetrics{}
	}
	if h.deadline <= 0 {
		h.deadline = DefaultDeadline
	}
	if h.now == nil {
		h.now = time.Now
	}
	return h
}

// ServeHTTP answers one delivery and records it, whatever the outcome.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := &Record{
		DeliveryID: r.Header.Get(HeaderDelivery),
		Event:      r.Header.Get(HeaderEvent),
		ReceivedAt: h.now(),
	}
	rec.Status = h.serve(r, w, rec)
	w.WriteHeader(rec.Status)
	h.metrics.Delivery(rec.App, rec.Event, string(rec.Outcome))
	h.sink.Record(context.WithoutCancel(r.Context()), rec)
}

// serve decides the answer and fills rec.
func (h *Handler) serve(r *http.Request, w http.ResponseWriter, rec *Record) int {
	reject := func(status int, err error) int {
		rec.Outcome, rec.Error = OutcomeRejected, err.Error()
		return status
	}
	if r.URL.Path != h.cfg.Ingest.Path {
		return reject(http.StatusNotFound, errors.New("not the ingest path"))
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		return reject(http.StatusMethodNotAllowed, errors.New("not a POST"))
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
	if err != nil {
		if tooLarge := new(http.MaxBytesError); errors.As(err, &tooLarge) {
			return reject(http.StatusRequestEntityTooLarge, err)
		}
		return reject(http.StatusBadRequest, err)
	}
	app, err := appFor(h.cfg, r.Header.Get(HeaderTargetID))
	if err != nil {
		return reject(http.StatusBadRequest, err)
	}
	rec.App = app.Name
	payload, err := verify(app, r.Header.Get(HeaderSignature), r.Header.Get("Content-Type"), body)
	if err != nil {
		h.log.Warn("rejected delivery", "delivery_id", rec.DeliveryID, "app", app.Name, "event", rec.Event, "error", err)
		return reject(http.StatusUnauthorized, err)
	}
	if rec.Event == "ping" {
		rec.Outcome = OutcomeIgnored
		return http.StatusOK
	}
	p, err := Parse(rec.Event, rec.DeliveryID, payload)
	if errors.Is(err, ErrUnknownEvent) {
		rec.Outcome = OutcomeIgnored
		return http.StatusAccepted
	}
	if err != nil {
		return reject(http.StatusBadRequest, err)
	}
	fill(rec, p, app)
	if rec.Own {
		rec.Outcome = OutcomeOwn
		return http.StatusAccepted
	}
	acts := Route(rec.Event, p, app, h.cfg.Renovate.ConfigPaths)
	rec.Outcome = h.run(r.Context(), acts, rec)
	if rec.Outcome == OutcomeFailed {
		return http.StatusServiceUnavailable
	}
	return http.StatusAccepted
}

// fill copies a verified payload's fields into rec.
func fill(rec *Record, p *Payload, app *config.App) {
	rec.Action = p.Action
	rec.InstallationID = p.InstallationID
	rec.SenderLogin, rec.SenderType = p.SenderLogin, p.SenderType
	rec.Own = Own(p, app)
	rec.Subject = p.subject()
	if p.Repo != nil {
		rec.RepoID, rec.RepoSlug = p.Repo.ID, p.Repo.Slug
	}
}

// run runs acts within the deadline: failed if any call failed, routed
// if any took effect, ignored when there was nothing to do or every
// signal found no workflow.
func (h *Handler) run(ctx context.Context, acts []Action, rec *Record) Outcome {
	if len(acts) == 0 {
		return OutcomeIgnored
	}
	// GitHub hanging up must not cut the batch short; the deadline does.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.deadline)
	defer cancel()
	out := OutcomeIgnored
	for i := range acts {
		ar := h.do(ctx, &acts[i])
		rec.Actions = append(rec.Actions, ar)
		switch {
		case ar.Error != "":
			out = OutcomeFailed
		case !ar.NotFound && out != OutcomeFailed:
			out = OutcomeRouted
		}
	}
	return out
}

// do runs one action.
func (h *Handler) do(ctx context.Context, a *Action) ActionRecord {
	ar := ActionRecord{Kind: a.Kind, WorkflowID: a.WorkflowID, Signal: a.Signal}
	start := time.Now()
	var err error
	switch a.Kind {
	case KindSignal:
		err = h.temporal.SignalWorkflow(ctx, a.WorkflowID, "", a.Signal, a.Arg)
		if notFound := new(serviceerror.NotFound); errors.As(err, &notFound) {
			ar.NotFound, err = true, nil
		}
	case KindSignalWithStart:
		state := *a.Start
		state.LastSeen = h.now()
		opts := client.StartWorkflowOptions{ID: a.WorkflowID, TaskQueue: h.taskQueue}
		_, err = h.temporal.SignalWithStartWorkflow(ctx, a.WorkflowID, a.Signal, a.Arg, opts, workflows.RepoWorkflowName, &state)
	case KindStartDiscovery:
		opts := client.StartWorkflowOptions{
			ID:                       a.WorkflowID,
			TaskQueue:                h.taskQueue,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		}
		_, err = h.temporal.ExecuteWorkflow(ctx, opts, workflows.DiscoveryWorkflowName, a.Arg)
	}
	h.metrics.TemporalCall(string(a.Kind), time.Since(start))
	if err != nil {
		ar.Error = err.Error()
		h.log.Warn("temporal call failed", "kind", a.Kind, "workflow_id", a.WorkflowID, "error", err)
	}
	return ar
}
