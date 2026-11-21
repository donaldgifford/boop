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
	"strconv"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/workflow"
)

func recordingPlan() *RunPlan {
	return &RunPlan{Profile: "default", Cadence: testCadence, Record: true}
}

func kinds(events []RecordedEvent) []string {
	out := make([]string, 0, len(events))
	for i := range events {
		out = append(out, events[i].Kind)
	}
	return out
}

// With store.enabled, a new repository records its onboarding, each run
// after its decision, the next run's schedule as that run's cause, and
// the offboarding the last run's result asks for, before it ends.
func TestRepoWorkflow_RecordsRunsAndDecisions(t *testing.T) {
	t.Parallel()
	p := newRW(t, succeeded()) // then out of script: disabled-no-config offboards
	p.plan = recordingPlan()
	p.keepSeen(3 * testCadence)
	if err := p.run(state(t0), 3*testCadence); err != nil {
		t.Fatalf("workflow: %v", err)
	}

	want := []string{KindRepoOnboarded, KindRepoRunScheduled, KindRepoOffboarded}
	if got := kinds(p.recorded); !slices.Equal(got, want) {
		t.Fatalf("recorded = %v, want %v", got, want)
	}
	seen := make(map[string]bool)
	for i := range p.recorded {
		e := &p.recorded[i]
		if parts := strings.Split(e.Key, ":"); len(parts) != 4 || parts[0] != "wf" || parts[3] != strconv.Itoa(i) || seen[e.Key] {
			t.Errorf("event %d key %q, want a unique wf:<workflow id>:<run id>:<seq>", i, e.Key)
		}
		seen[e.Key] = true
		if e.RepoID != testRepoID || e.InstallationID != 1 || e.OccurredAt.IsZero() {
			t.Errorf("event %d = %+v, want the repository, installation and time", i, e)
		}
	}
	if r := p.recorded[0].Repository; r == nil || r.Via != ViaDiscovery || r.Slug != "acme/app" {
		t.Errorf("onboarded = %+v, want via discovery", r)
	}
	if r := p.recorded[2].Repository; r == nil || r.Reason != "disabled-no-config" {
		t.Errorf("offboarded = %+v, want the run's result as the reason", r)
	}

	if len(p.recordedRuns) != 2 {
		t.Fatalf("RecordRun calls = %d, want 2", len(p.recordedRuns))
	}
	first, second := p.recordedRuns[0], p.recordedRuns[1]
	if first.Outcome != OutcomeSucceeded || first.Result == nil || first.Slug != "acme/app" || first.CauseKey != "" {
		t.Errorf("first RecordRun = %+v", first)
	}
	if !first.StartedAt.Equal(p.runAt[0]) || first.QueuedAt.After(first.StartedAt) || !strings.HasSuffix(first.RunID, ":1") {
		t.Errorf("first RecordRun times and id = %+v", first)
	}
	if second.Outcome != OutcomeSkipped || second.CauseKey != p.recorded[1].Key || first.RunID == second.RunID {
		t.Errorf("second RecordRun = %+v, want caused by the run-scheduled %q", second, p.recorded[1].Key)
	}
}

// With store.enabled false, PlanRun's Record is false and no store
// activity is scheduled.
func TestRepoWorkflow_NoRecordWhenDisabled(t *testing.T) {
	t.Parallel()
	p := newRW(t, succeeded())
	p.keepSeen(3 * testCadence)
	if err := p.run(state(t0), 3*testCadence); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if len(p.runs) != 2 || len(p.recorded) != 0 || len(p.recordedRuns) != 0 {
		t.Errorf("runs %d, recorded %d events and %d runs; want 2 runs and no records", len(p.runs), len(p.recorded), len(p.recordedRuns))
	}
}

// An execution on the old version records nothing, even with the store
// enabled.
func TestRepoWorkflow_RecordVersionGate(t *testing.T) {
	t.Parallel()
	p := newRW(t, succeeded())
	p.plan = recordingPlan()
	p.env.OnGetVersion(storeRecordChange, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	p.keepSeen(3 * testCadence)
	if err := p.run(state(t0), 3*testCadence); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if len(p.recorded) != 0 || len(p.recordedRuns) != 0 {
		t.Errorf("recorded %d events and %d runs on the old version, want none", len(p.recorded), len(p.recordedRuns))
	}
}

// A recheck records action.recheck with the payload's actor, or none
// when the signal has no payload, and the run it starts names it as its
// cause (OQ9).
func TestRepoWorkflow_RecheckPayload(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		payload any
		actor   string
	}{
		{"with an actor", Recheck{Actor: "dg@example.com", RequestID: "req-1"}, "dg@example.com"},
		{"no payload", nil, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newRW(t, succeeded(), succeeded())
			p.plan = recordingPlan()
			in := state(t0)
			in.NextDue = t0.Add(testCadence)
			p.env.RegisterDelayedCallback(func() { p.env.SignalWorkflow(RecheckSignal, tt.payload) }, time.Hour)
			p.keepSeen(testCadence / 2)
			if err := p.run(in, testCadence/2); err != nil && !isCanceled(err) {
				t.Fatalf("workflow: %v", err)
			}
			i := slices.Index(kinds(p.recorded), KindActionRecheck)
			if i < 0 {
				t.Fatalf("recorded = %v, want an action.recheck", kinds(p.recorded))
			}
			e := p.recorded[i]
			switch {
			case tt.actor == "" && e.Actor != nil:
				t.Errorf("actor = %+v, want none", e.Actor)
			case tt.actor != "" && (e.Actor == nil || e.Actor.Login != tt.actor || e.Actor.Type != ActorOIDC || e.Recheck.RequestID != "req-1"):
				t.Errorf("recheck = %+v %+v, want %s", e.Actor, e.Recheck, tt.actor)
			}
			if len(p.recordedRuns) == 0 || p.recordedRuns[0].CauseKey != e.Key {
				t.Errorf("RecordRun = %+v, want caused by %q", p.recordedRuns, e.Key)
			}
		})
	}
}

// A budget wait and a capacity denial are recorded once per acquire,
// and a stall when the stall limit is reached.
func TestRepoWorkflow_RecordsWaitsAndStall(t *testing.T) {
	t.Parallel()
	p := newRW(t, timedOut(0), timedOut(0), timedOut(0), timedOut(0))
	p.plan = recordingPlan()
	p.grantAt = []time.Time{t0.Add(time.Minute), t0.Add(2 * time.Minute)}
	p.capDenials = 2
	p.keepSeen(testCadence / 2)
	if err := p.run(state(t0), testCadence/2); err != nil && !isCanceled(err) {
		t.Fatalf("workflow: %v", err)
	}
	got := kinds(p.recorded)
	count := func(k string) int {
		n := 0
		for _, g := range got {
			if g == k {
				n++
			}
		}
		return n
	}
	if count(KindRunBudgetWait) != 1 || count(KindRunCapacityDenied) != 1 || count(KindRunStalled) != 1 {
		t.Errorf("recorded = %v, want one budget-wait, one capacity-denied and one stall", got)
	}
	if len(p.recordedRuns) != StallLimit+1 {
		t.Errorf("RecordRun calls = %d, want every timed-out attempt (%d)", len(p.recordedRuns), StallLimit+1)
	}
}
