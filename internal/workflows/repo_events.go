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
	"slices"
	"time"

	"go.temporal.io/sdk/workflow"
)

// Webhook signals on RepoWorkflow (DESIGN-0003 § RepoWorkflow: event and
// changed). Existing histories hold neither signal, and these paths run
// only when one arrives, so they add no command to an old history.

// applyEvent schedules the run an event asks for: settle after now, and
// for a normal-priority event no sooner than min_interval after the last
// run's start. A pending event keeps the earlier due time and the
// highest priority, so an event only ever brings a run forward.
func (r *repo) applyEvent(ctx workflow.Context, e *Event) {
	prio := e.Priority
	if prio == 0 {
		prio = PriorityNormal
	}
	due := workflow.Now(ctx).Add(r.settle)
	if prio != PriorityHigh && !r.lastStart.IsZero() {
		if floor := r.lastStart.Add(r.minInterval); floor.After(due) {
			due = floor
		}
	}
	if r.s.EventDue.IsZero() || due.Before(r.s.EventDue) {
		r.s.EventDue, r.s.EventReason = due, e.Reason
	}
	if r.s.EventPriority == 0 || prio < r.s.EventPriority {
		r.s.EventPriority = prio
	}
	workflow.GetLogger(ctx).Info("run-scheduled", "repo_id", r.s.RepoID, "reason", e.Reason,
		"priority", int(prio), "at", r.s.EventDue, "cause", e.DeliveryID)
}

// drainEvents applies every buffered event, such as those received
// during a run.
func (r *repo) drainEvents(ctx workflow.Context) {
	var e Event
	for r.event.ReceiveAsync(&e) {
		r.applyEvent(ctx, &e)
		e = Event{}
	}
}

// takeChanged receives one buffered changed signal.
func (r *repo) takeChanged() (Changed, bool) {
	var c Changed
	ok := r.changed.ReceiveAsync(&c)
	return c, ok
}

// due is when the next run is due and at what priority: an event's run
// when it is due no later than the cadence's.
func (r *repo) due() (time.Time, Priority, string) {
	if !r.s.EventDue.IsZero() && !r.s.EventDue.After(r.s.NextDue) {
		return r.s.EventDue, r.s.EventPriority, r.s.EventReason
	}
	return r.s.NextDue, PriorityNormal, ""
}

// clearEvent forgets the pending event: the run starting now covers it.
func (r *repo) clearEvent() {
	r.s.EventDue, r.s.EventPriority, r.s.EventReason = time.Time{}, 0, ""
}

// handleChanged runs CheckRepo for a changed signal. Gone or no config
// ends the workflow (end). Present refreshes the slug, branch and
// extends, re-plans the profile and schedules a run as a normal-priority
// event. A failed check schedules the run too: the run itself offboards
// a repository that lost its config.
func (r *repo) handleChanged(ctx workflow.Context, c *Changed) (end bool) {
	log := workflow.GetLogger(ctx)
	if c.InstallationID != 0 {
		r.s.InstallationID = c.InstallationID
	}
	if c.Slug != "" {
		r.s.Slug = c.Slug
	}
	if c.DefaultBranch != "" {
		r.s.DefaultBranch = c.DefaultBranch
	}
	ev := &Event{Reason: EventReasonChanged, Priority: PriorityNormal, DeliveryID: c.DeliveryID}
	res, err := r.check(ctx)
	if err != nil {
		log.Warn("CheckRepo failed on changed; scheduling a run", "repo_id", r.s.RepoID, "reason", c.Reason, "error", err)
		r.applyEvent(ctx, ev)
		return false
	}
	if res.State == RepoGone || res.State == RepoNoConfig {
		reason := res.State
		if c.Reason == ChangedReasonRemoved || c.Reason == ChangedReasonInstallationDeleted {
			reason = c.Reason
		}
		log.Info("offboarded", "repo_id", r.s.RepoID, "reason", reason, "cause", c.DeliveryID)
		return true
	}
	if res.Slug != "" {
		r.s.Slug, r.s.DefaultBranch = res.Slug, res.DefaultBranch
	}
	r.s.Extends = slices.Clone(res.Extends)
	r.s.LastSeen = workflow.Now(ctx)
	r.nextCheck = time.Time{}
	log.Info("refreshed", "repo_id", r.s.RepoID, "slug", r.s.Slug, "default_branch", r.s.DefaultBranch,
		"extends", r.s.Extends, "reason", c.Reason, "cause", c.DeliveryID)
	if r.onboarding {
		r.onboarding = false
		log.Info("onboarded", "repo_id", r.s.RepoID, "via", "webhook", "cause", c.DeliveryID)
	}
	if err := r.plan(ctx, PriorityNormal); err != nil {
		log.Warn("re-plan after changed failed; the run plans again", "repo_id", r.s.RepoID, "error", err)
	}
	r.applyEvent(ctx, ev)
	return false
}

// countEventRun counts a run an event scheduled, by its reason.
func countEventRun(ctx workflow.Context, reason string) {
	if reason == "" {
		return
	}
	workflow.GetMetricsHandler(ctx).WithTags(map[string]string{"reason": reason}).Counter(MetricRepoEventRuns).Inc(1)
}
