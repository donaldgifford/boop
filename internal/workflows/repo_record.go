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
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"
)

// storeRecordChange gates the store activities (IMPL-0005 5.9), so
// RepoWorkflows and DiscoveryWorkflows started before it replay without
// them.
const storeRecordChange = "store-record"

// storeBackfillChange gates backfill: executions started before it
// replay without the onboarding it records.
const storeBackfillChange = "store-backfill"

// Recording (DESIGN-0004 § Workflow and activity changes). Every store
// call is an activity the workflow does not wait for: recording never
// holds up a run. Futures still in flight are waited for before the
// workflow ends or continues as new, so nothing is dropped.

// recording is true when this execution is past the version gate and
// the last plan said store.enabled.
func (r *repo) recording() bool { return r.recordV >= 1 && r.record }

// note records workflow decisions and returns the last one's key. Each
// key is wf:<workflow id>:<run id>:<seq>, with seq carried through
// ContinueAsNew.
func (r *repo) note(ctx workflow.Context, events ...RecordedEvent) string {
	if !r.recording() || len(events) == 0 {
		return ""
	}
	info := workflow.GetInfo(ctx)
	now := workflow.Now(ctx)
	key := ""
	for i := range events {
		e := &events[i]
		key = fmt.Sprintf("wf:%s:%s:%d", info.WorkflowExecution.ID, info.WorkflowExecution.RunID, r.s.RecordSeq)
		r.s.RecordSeq++
		e.Key = key
		if e.OccurredAt.IsZero() {
			e.OccurredAt = now
		}
		if e.RepoID == 0 {
			e.RepoID = r.s.RepoID
		}
		if e.InstallationID == 0 {
			e.InstallationID = r.s.InstallationID
		}
	}
	r.track(workflow.ExecuteActivity(storeOptions(ctx, r.s.InstallationID), RecordEventsActivity, &RecordEventsInput{Events: events}))
	return key
}

// track keeps a record future until it is ready.
func (r *repo) track(f workflow.Future) {
	kept := r.records[:0]
	for _, p := range r.records {
		if !p.IsReady() {
			kept = append(kept, p)
		}
	}
	kept = append(kept, f)
	r.records = kept
}

// flushRecords waits for every record in flight. The activities retry
// without limit, so a store outage holds the workflow here, not a run.
func (r *repo) flushRecords(ctx workflow.Context) {
	for _, f := range r.records {
		if err := f.Get(ctx, nil); err != nil {
			workflow.GetLogger(ctx).Warn("record failed", "repo_id", r.s.RepoID, "error", err)
		}
	}
	r.records = nil
}

func (r *repo) repository(via, reason string) *RecordRepository {
	return &RecordRepository{Slug: r.s.Slug, Profile: r.s.Profile, Via: via, Reason: reason}
}

// backfill records a repository the store has not seen: an execution
// that continued as new from one that predates recording (an upgrade
// that enabled the store) records its onboarding and schedule once, so
// its projections exist before its next run.
func (r *repo) backfill(ctx workflow.Context) {
	if r.backfillV < 1 || !r.recording() || r.s.Recorded {
		return
	}
	r.noteOnboarded(ctx, ViaDiscovery, "")
	if !r.s.NextDue.IsZero() {
		r.noteScheduled(ctx, r.s.NextDue, PriorityNormal, "")
	}
}

// noteOnboarded records repo.onboarded.
func (r *repo) noteOnboarded(ctx workflow.Context, via, cause string) {
	if r.recording() {
		r.s.Recorded = true
	}
	r.note(ctx, RecordedEvent{
		Kind: KindRepoOnboarded, CauseKey: cause, Summary: r.s.Slug + " onboarded via " + via, Repository: r.repository(via, ""),
	})
}

// noteOffboarded records repo.offboarded.
func (r *repo) noteOffboarded(ctx workflow.Context, reason, cause string) {
	r.note(ctx, RecordedEvent{
		Kind: KindRepoOffboarded, CauseKey: cause, Summary: r.s.Slug + " offboarded: " + reason, Repository: r.repository("", reason),
	})
}

// noteScheduled records repo.run-scheduled; its key becomes the next
// run's cause.
func (r *repo) noteScheduled(ctx workflow.Context, at time.Time, prio Priority, reason string) {
	summary := "next run " + at.UTC().Format(time.RFC3339)
	if reason != "" {
		summary += " (" + reason + ")"
	}
	if key := r.note(ctx, RecordedEvent{
		Kind: KindRepoRunScheduled, Summary: summary, Scheduled: &RecordScheduled{NextDue: at, Priority: prio, Reason: reason},
	}); key != "" {
		r.s.CauseKey = key
	}
}

// noteState records a wait or a stall.
func (r *repo) noteState(ctx workflow.Context, kind, summary string, until time.Time) {
	r.note(ctx, RecordedEvent{Kind: kind, Summary: summary, State: &RecordState{Reason: summary, Until: until}})
}

// noteRecheck records who asked for a recheck; its key becomes the next
// run's cause.
func (r *repo) noteRecheck(ctx workflow.Context, rc *Recheck) {
	e := RecordedEvent{Kind: KindActionRecheck, Summary: "recheck requested", Recheck: &RecordRecheck{RequestID: rc.RequestID}}
	if rc.Actor != "" {
		e.Actor = &RecordActor{Login: rc.Actor, Type: ActorOIDC}
		e.Summary = "recheck requested by " + rc.Actor
	}
	if key := r.note(ctx, e); key != "" {
		r.s.CauseKey = key
	}
}

// recordRun records one attempt with an outcome, after the decision.
func (r *repo) recordRun(ctx workflow.Context, a *attempt, queued, start time.Time) {
	if !r.recording() {
		return
	}
	finished := workflow.Now(ctx)
	if r.s.LastRun != nil {
		finished = r.s.LastRun.FinishedAt
	}
	r.track(workflow.ExecuteActivity(storeOptions(ctx, r.s.InstallationID), RecordRunActivity, &RecordRunInput{
		RunID:  fmt.Sprintf("%s:%d", workflow.GetInfo(ctx).WorkflowExecution.RunID, r.s.Iterations),
		RepoID: r.s.RepoID, InstallationID: r.s.InstallationID, Slug: r.s.Slug, CauseKey: r.s.CauseKey,
		QueuedAt: queued, StartedAt: start, FinishedAt: finished, Outcome: a.outcome, Result: a.result,
	}))
}
