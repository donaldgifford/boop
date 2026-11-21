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
	"fmt"
	"strings"

	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/workflows"
)

// RecordRun appends a run's run.started and run.finished (DESIGN-0004 §
// Append and apply). The keys are run:<run id>:started and
// run:<run id>:finished, so a retry is a no-op. With the store disabled
// it does nothing.
func (a *Activities) RecordRun(ctx context.Context, in *workflows.RecordRunInput) error {
	if a.store == nil {
		return nil
	}
	owner, _, _ := strings.Cut(in.Slug, "/")
	base := store.Event{
		Source: store.SourceWorker, InstallationID: in.InstallationID, RepoID: in.RepoID, CauseKey: in.CauseKey,
	}
	started := base
	started.Key, started.OccurredAt, started.Kind = "run:"+in.RunID+":started", in.StartedAt, store.KindRunStarted
	started.Summary = "run started"
	started.Data = &store.RunStarted{
		V: 1, RunID: in.RunID, Owner: owner, Slug: in.Slug, QueuedMS: in.StartedAt.Sub(in.QueuedAt).Milliseconds(),
	}
	finished := base
	finished.Key, finished.OccurredAt, finished.Kind = "run:"+in.RunID+":finished", in.FinishedAt, store.KindRunFinished
	finished.Summary = "run " + strings.ToLower(string(in.Outcome))
	finished.Data = runFinished(in, owner)
	if err := a.store.Append(ctx, started, finished); err != nil {
		return fmt.Errorf("record run %s: %w", in.RunID, err)
	}
	return nil
}

// runFinished maps the run's result to run.finished's payload.
func runFinished(in *workflows.RecordRunInput, owner string) *store.RunFinished {
	d := &store.RunFinished{
		V: 1, RunID: in.RunID, Owner: owner, Slug: in.Slug, StartedAt: in.StartedAt, FinishedAt: in.FinishedAt,
		Outcome: string(in.Outcome), ReportMissing: true,
	}
	res := in.Result
	if res == nil {
		return d
	}
	d.RepositoryResult, d.RenovateVersion, d.ReportMissing = res.RepositoryResult, res.RenovateVersion, res.ReportMissing
	d.Spend = spend(res.RateBefore, res.RateAfter)
	for _, p := range res.Problems {
		d.Problems = append(d.Problems, p.Message)
	}
	for i := range res.Tuples {
		t := &res.Tuples[i]
		d.Updates = append(d.Updates, store.BranchUpdate{
			Branch: t.BranchName, PRNumber: t.PRNumber, PackageFile: t.PackageFile,
			PackageName: firstNonEmpty(t.PackageName, t.DepName), DepName: t.DepName,
			Manager: t.Manager, Datasource: t.Datasource, UpdateType: t.UpdateType,
			FromVersion: t.CurrentVersion, ToVersion: t.NewVersion,
		})
	}
	for i := range res.Dependencies {
		dep := &res.Dependencies[i]
		d.Dependencies = append(d.Dependencies, store.Dependency{
			Manager: dep.Manager, PackageFile: dep.PackageFile, PackageName: dep.PackageName, DepName: dep.DepName,
			Datasource: dep.Datasource, CurrentValue: dep.CurrentValue, CurrentVersion: dep.CurrentVersion,
		})
	}
	return d
}

// spend is each tracked resource's remaining before and after the run,
// when both readings have it.
func spend(before, after *workflows.Readings) map[string]store.Spend {
	if before == nil || after == nil {
		return nil
	}
	out := make(map[string]store.Spend)
	for _, r := range workflows.TrackedResources {
		b, okB := before.Resources[r]
		f, okA := after.Resources[r]
		if okB && okA {
			out[r] = store.Spend{Before: b.Remaining, After: f.Remaining}
		}
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// RecordEvents appends workflow decisions (DESIGN-0004 § Event
// catalogue). Each carries the workflow's key, so a retry or a replay
// is a no-op. With the store disabled it does nothing.
func (a *Activities) RecordEvents(ctx context.Context, in *workflows.RecordEventsInput) error {
	if a.store == nil || len(in.Events) == 0 {
		return nil
	}
	events := make([]store.Event, 0, len(in.Events))
	for i := range in.Events {
		e, err := recordedEvent(&in.Events[i])
		if err != nil {
			return err
		}
		events = append(events, e)
	}
	if err := a.store.Append(ctx, events...); err != nil {
		return fmt.Errorf("record %d events: %w", len(events), err)
	}
	return nil
}

// recordedEvent maps one workflow decision to a store event.
func recordedEvent(r *workflows.RecordedEvent) (store.Event, error) {
	e := store.Event{
		Key: r.Key, OccurredAt: r.OccurredAt, Kind: store.Kind(r.Kind), Source: store.SourceWorker,
		InstallationID: r.InstallationID, RepoID: r.RepoID, CauseKey: r.CauseKey, Summary: r.Summary,
	}
	if r.Actor != nil {
		e.Actor = store.Actor{Login: r.Actor.Login, Type: r.Actor.Type}
	}
	switch {
	case r.Repository != nil:
		owner, _, _ := strings.Cut(r.Repository.Slug, "/")
		e.Data = &store.Repository{
			V: 1, Owner: owner, Slug: r.Repository.Slug, Profile: r.Repository.Profile, Via: r.Repository.Via, Reason: r.Repository.Reason,
		}
	case r.Scheduled != nil:
		e.Data = &store.RunScheduled{V: 1, NextDue: r.Scheduled.NextDue, Priority: priorityName(r.Scheduled.Priority), Reason: r.Scheduled.Reason}
	case r.State != nil:
		d := &store.RepoState{V: 1, Reason: r.State.Reason}
		if !r.State.Until.IsZero() {
			until := r.State.Until
			d.Until = &until
		}
		e.Data = d
	case r.Recheck != nil:
		e.Data = &store.Recheck{V: 1, RequestID: r.Recheck.RequestID}
	case r.Discovery != nil:
		d := r.Discovery
		e.Data = &store.DiscoveryFinished{
			V: 1, App: d.App, StartedAt: d.StartedAt, FinishedAt: d.FinishedAt,
			Seen: d.Seen, Onboarded: d.Onboarded, Failed: d.Failed, ProbeCost: d.ProbeCost,
		}
	default:
		return store.Event{}, fmt.Errorf("event %s (%s) has no payload", r.Key, r.Kind)
	}
	return e, nil
}

// priorityName is a priority as the timeline shows it.
func priorityName(p workflows.Priority) string {
	switch p {
	case workflows.PriorityHigh:
		return "high"
	case workflows.PriorityNormal:
		return "normal"
	case workflows.PriorityLow:
		return "low"
	default:
		return ""
	}
}
